package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

// InvitationEvent es el tipo de evento del outbox que entrega el correo de invitación.
const InvitationEvent = "organizations.invitation.requested.v1"

// invitationTTL se calcula en PostgreSQL con el mismo valor de domain.InvitationTTL.
const invitationTTLSQL = `interval '168 hours'`

func (s *store) auditMeta(ctx context.Context, actor string, org, action, kind, id string, meta map[string]any) error {
	raw, e := json.Marshal(meta)
	if e != nil {
		return e
	}
	_, e = s.tx.Exec(ctx, `INSERT INTO audit_events(organization_id,actor_user_id,action,entity_type,entity_id,metadata,created_by,updated_by) VALUES(NULLIF($1,''),$2,$3,$4,$5,$6::jsonb,$2,$2)`, org, actor, action, kind, id, string(raw))
	return e
}

// LockForManagement autoriza sin bloquear, bloquea la organización (FOR UPDATE) y
// vuelve a autorizar con el bloqueo tomado. Todos los cambios de cuentas de una
// organización quedan serializados, lo que evita interbloqueos y carreras del último ADMIN.
func (s *store) LockForManagement(ctx context.Context, a domain.Actor, org string) (string, error) {
	var ok bool
	var e error
	if a.Role == "SUPER_ADMIN" {
		e = s.tx.QueryRow(ctx, `SELECT true FROM organizations WHERE id=$1 AND status='ACTIVE'`, org).Scan(&ok)
	} else {
		e = s.tx.QueryRow(ctx, `SELECT true FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id AND o.status='ACTIVE' WHERE m.organization_id=$1 AND m.user_id=$2 AND m.status='ACTIVE' AND m.role='ADMIN'`, org, a.ID).Scan(&ok)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return "", domain.ErrDenied
	}
	if e != nil {
		return "", e
	}
	var id string
	if e = s.tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 AND status='ACTIVE' FOR UPDATE`, org).Scan(&id); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return "", domain.ErrDenied
		}
		return "", e
	}
	role, e := s.Role(ctx, a, org)
	if e != nil {
		return "", e
	}
	if !domain.CanManage(role) {
		return "", domain.ErrDenied
	}
	return role, nil
}

func (s *store) LockOrganizationRow(ctx context.Context, org string) error {
	var id string
	e := s.tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 AND status='ACTIVE' FOR UPDATE`, org).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return e
}

func (s *store) LockOrganization(ctx context.Context, org string) (o domain.Organization, e error) {
	e = s.tx.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations o WHERE o.id=$1`, org).Scan(&o.ID, &o.LegalName, &o.TradeName, &o.TaxID, &o.BusinessType, &o.ContactName, &o.ContactPhone, &o.ContactEmail, &o.RowVersion)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return
}

func (s *store) SaveOrganization(ctx context.Context, a domain.Actor, o domain.Organization, fields []string) (out domain.Organization, e error) {
	e = s.tx.QueryRow(ctx, `UPDATE organizations o SET legal_name=$2,trade_name=$3,tax_id=NULLIF($4,''),business_type=$5,contact_name=$6,contact_phone=$7,contact_email=$8,row_version=row_version+1,updated_by=$9,updated_at=NOW() WHERE o.id=$1 RETURNING `+orgColumns,
		o.ID, o.LegalName, o.TradeName, o.TaxID, o.BusinessType, o.ContactName, o.ContactPhone, o.ContactEmail, a.ID).
		Scan(&out.ID, &out.LegalName, &out.TradeName, &out.TaxID, &out.BusinessType, &out.ContactName, &out.ContactPhone, &out.ContactEmail, &out.RowVersion)
	if e != nil {
		return out, conflict(e)
	}
	return out, s.auditMeta(ctx, a.ID, o.ID, "organization.updated", "organization", o.ID, map[string]any{"fields": fields})
}

func (s *store) LockLocation(ctx context.Context, org, id string) (l domain.Location, e error) {
	e = s.tx.QueryRow(ctx, `SELECT l.id::text,l.name,l.address,COALESCE(l.district,''),COALESCE(l.contact_phone,''),l.timezone,l.status,l.row_version FROM locations l WHERE l.organization_id=$1 AND l.id=$2 FOR UPDATE`, org, id).
		Scan(&l.ID, &l.Name, &l.Address, &l.District, &l.ContactPhone, &l.Timezone, &l.Status, &l.RowVersion)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return
}

func (s *store) SaveLocation(ctx context.Context, a domain.Actor, org string, l domain.Location, fields []string) (out domain.Location, e error) {
	e = s.tx.QueryRow(ctx, `UPDATE locations SET name=$3,address=$4,district=$5,contact_phone=$6,timezone=$7,status=$8,row_version=row_version+1,updated_by=$9,updated_at=NOW() WHERE organization_id=$1 AND id=$2 RETURNING id::text,name,address,COALESCE(district,''),COALESCE(contact_phone,''),timezone,status,row_version`,
		org, l.ID, l.Name, l.Address, l.District, l.ContactPhone, l.Timezone, l.Status, a.ID).
		Scan(&out.ID, &out.Name, &out.Address, &out.District, &out.ContactPhone, &out.Timezone, &out.Status, &out.RowVersion)
	if e != nil {
		return out, conflict(e)
	}
	return out, s.auditMeta(ctx, a.ID, org, "location.updated", "location", l.ID, map[string]any{"fields": fields, "status": l.Status})
}

const memberSelect = `SELECT u.id,u.display_name,u.email,m.role,COALESCE((SELECT array_agg(ml.location_id::text ORDER BY ml.location_id) FROM membership_locations ml WHERE ml.organization_id=m.organization_id AND ml.user_id=u.id),'{}'::text[]),m.status,m.row_version,(u.status='ACTIVE')
 FROM organization_memberships m JOIN application_users u ON u.id=m.user_id WHERE m.organization_id=$1 AND m.user_id=$2`

func (s *store) LockMember(ctx context.Context, org, uid string) (m domain.Member, e error) {
	e = s.tx.QueryRow(ctx, memberSelect+` FOR UPDATE OF m`, org, uid).Scan(&m.ID, &m.Name, &m.Email, &m.Role, &m.LocationIDs, &m.Status, &m.RowVersion, &m.AccountActive)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return
}

func (s *store) CountOtherActiveAdmins(ctx context.Context, org, exclude string) (n int, e error) {
	e = s.tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE' WHERE m.organization_id=$1 AND m.role='ADMIN' AND m.status='ACTIVE' AND m.user_id<>$2`, org, exclude).Scan(&n)
	return
}

func (s *store) CheckLocations(ctx context.Context, org string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var n int
	if e := s.tx.QueryRow(ctx, `SELECT count(*) FROM locations WHERE organization_id=$1 AND id=ANY($2::uuid[]) AND status='ACTIVE'`, org, ids).Scan(&n); e != nil {
		return e
	}
	if n != len(ids) {
		return &domain.ValidationError{Fields: map[string]string{"locationIds": "Una sede no pertenece a esta organización o está inactiva."}}
	}
	return nil
}

func (s *store) CountMemberLocations(ctx context.Context, org, uid string) (n int, e error) {
	e = s.tx.QueryRow(ctx, `SELECT count(*) FROM membership_locations ml JOIN locations l ON l.organization_id=ml.organization_id AND l.id=ml.location_id AND l.status='ACTIVE' WHERE ml.organization_id=$1 AND ml.user_id=$2`, org, uid).Scan(&n)
	return
}

func (s *store) SaveMember(ctx context.Context, a domain.Actor, org, uid string, c domain.MemberChange) (domain.Member, error) {
	if _, e := s.tx.Exec(ctx, `UPDATE organization_memberships SET role=$3,status=$4,row_version=row_version+1,updated_by=$5,updated_at=NOW() WHERE organization_id=$1 AND user_id=$2`, org, uid, c.Role, c.Status, a.ID); e != nil {
		return domain.Member{}, e
	}
	if c.Locations != nil {
		if _, e := s.tx.Exec(ctx, `DELETE FROM membership_locations WHERE organization_id=$1 AND user_id=$2`, org, uid); e != nil {
			return domain.Member{}, e
		}
		for _, id := range *c.Locations {
			if _, e := s.tx.Exec(ctx, `INSERT INTO membership_locations(organization_id,user_id,location_id,created_by,updated_by) VALUES($1,$2,$3,$4,$4)`, org, uid, id, a.ID); e != nil {
				return domain.Member{}, e
			}
		}
	}
	if c.Status == "INACTIVE" {
		if e := s.cutAccess(ctx, a, org, uid); e != nil {
			return domain.Member{}, e
		}
	}
	if e := s.auditMeta(ctx, a.ID, org, c.Action, "membership", uid, map[string]any{"fields": c.Fields, "role": c.Role, "status": c.Status}); e != nil {
		return domain.Member{}, e
	}
	var m domain.Member
	e := s.tx.QueryRow(ctx, memberSelect, org, uid).Scan(&m.ID, &m.Name, &m.Email, &m.Role, &m.LocationIDs, &m.Status, &m.RowVersion, &m.AccountActive)
	return m, e
}

// cutAccess retira dispositivos de la organización y revoca las sesiones técnicas
// (authentication_sessions) cuando la cuenta ya no tiene otra membresía activa ni
// rol de plataforma: las sesiones no están ligadas a un tenant, así que revocarlas
// con otra membresía vigente cerraría accesos legítimos. El acceso al tenant se
// niega además en cada solicitud porque la membresía se vuelve a consultar.
func (s *store) cutAccess(ctx context.Context, a domain.Actor, org, uid string) error {
	if _, e := s.tx.Exec(ctx, `UPDATE mobile_devices SET status='INACTIVE',push_token=NULL,token_hash=NULL,updated_by=$3,updated_at=NOW() WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE'`, org, uid, a.ID); e != nil {
		return e
	}
	_, e := s.tx.Exec(ctx, `UPDATE authentication_sessions SET revoked=true WHERE user_id=$1 AND NOT revoked
 AND NOT EXISTS(SELECT 1 FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id AND o.status='ACTIVE' WHERE m.user_id=$1 AND m.status='ACTIVE')
 AND NOT EXISTS(SELECT 1 FROM application_users u WHERE u.id=$1 AND u.platform_role='SUPER_ADMIN')`, uid)
	return e
}

// ---------------------------------------------------------------- invitaciones

const invitationSelect = `SELECT i.id::text,i.organization_id,i.email,i.role,i.location_ids::text[],
 CASE WHEN i.status='PENDING' AND i.expires_at<=NOW() THEN 'EXPIRED' ELSE i.status END,
 i.expires_at,i.last_sent_at,i.created_at,i.send_count,i.row_version,COALESCE(i.accepted_by,'') FROM organization_invitations i`

func scanInvitation(row pgx.Row) (v domain.Invitation, e error) {
	e = row.Scan(&v.ID, &v.OrganizationID, &v.Email, &v.Role, &v.LocationIDs, &v.Status, &v.ExpiresAt, &v.LastSentAt, &v.CreatedAt, &v.SendCount, &v.RowVersion, &v.AcceptedBy)
	v.ExpiresAt, v.LastSentAt, v.CreatedAt = v.ExpiresAt.UTC(), v.LastSentAt.UTC(), v.CreatedAt.UTC()
	return
}

func (s *store) scrubInvitationEvents(ctx context.Context, org, id string) error {
	_, e := s.tx.Exec(ctx, `UPDATE outbox_events SET payload='{"scrubbed":true}'::jsonb,
 status=CASE WHEN status='PENDING' THEN 'FAILED' ELSE status END,
 last_error=CASE WHEN status='PENDING' THEN 'SUPERSEDED' ELSE last_error END,updated_at=NOW()
 WHERE organization_id=$1 AND aggregate_type='organization_invitation' AND aggregate_id=$2 AND event_type=$3`, org, id, InvitationEvent)
	return e
}

func (s *store) CreateInvitation(ctx context.Context, a domain.Actor, org string, in domain.Invitation) (domain.Invitation, bool, error) {
	// Las pendientes vencidas dejan libre el correo para una invitación nueva.
	if _, e := s.tx.Exec(ctx, `UPDATE organization_invitations SET status='EXPIRED',row_version=row_version+1,updated_at=NOW() WHERE organization_id=$1 AND LOWER(email)=$2 AND status='PENDING' AND expires_at<=NOW()`, org, in.Email); e != nil {
		return in, false, e
	}
	existing, e := scanInvitation(s.tx.QueryRow(ctx, invitationSelect+` WHERE i.id=$1`, in.ID))
	if e == nil {
		same := existing.OrganizationID == org && existing.Email == in.Email && existing.Role == in.Role
		got, want := slices.Clone(existing.LocationIDs), slices.Clone(in.LocationIDs)
		slices.Sort(got)
		slices.Sort(want)
		if !same || !slices.Equal(got, want) {
			return in, false, domain.ErrConflict
		}
		var by string
		if e = s.tx.QueryRow(ctx, `SELECT COALESCE(created_by,'') FROM organization_invitations WHERE id=$1`, in.ID).Scan(&by); e != nil {
			return in, false, e
		}
		if by != a.ID {
			return in, false, domain.ErrConflict
		}
		return existing, false, nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return in, false, e
	}
	var member bool
	if e = s.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships m JOIN application_users u ON u.id=m.user_id WHERE m.organization_id=$1 AND LOWER(u.email)=$2)`, org, in.Email).Scan(&member); e != nil {
		return in, false, e
	}
	if member {
		return in, false, domain.ErrMemberExists
	}
	var pending bool
	if e = s.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_invitations WHERE organization_id=$1 AND LOWER(email)=$2 AND status='PENDING')`, org, in.Email).Scan(&pending); e != nil {
		return in, false, e
	}
	if pending {
		return in, false, domain.ErrInvitationPending
	}
	locs := in.LocationIDs
	if locs == nil {
		locs = []string{}
	}
	if _, e = s.tx.Exec(ctx, `INSERT INTO organization_invitations(id,organization_id,email,role,location_ids,token_hash,expires_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5::uuid[],$6,NOW()+`+invitationTTLSQL+`,$7,$7)`, in.ID, org, in.Email, in.Role, locs, in.TokenHash, a.ID); e != nil {
		return in, false, conflict(e)
	}
	out, e := scanInvitation(s.tx.QueryRow(ctx, invitationSelect+` WHERE i.id=$1`, in.ID))
	if e != nil {
		return out, false, e
	}
	return out, true, s.auditMeta(ctx, a.ID, org, "invitation.created", "invitation", in.ID, map[string]any{"role": in.Role})
}

// QueueInvitationEmail registra el correo en el outbox dentro de la misma transacción.
// El token sin cifrar solo existe en este evento y se borra al enviarlo o sustituirlo.
func (s *store) QueueInvitationEmail(ctx context.Context, a domain.Actor, inv domain.Invitation, token string) error {
	var name string
	if e := s.tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(trade_name,''),legal_name) FROM organizations WHERE id=$1`, inv.OrganizationID).Scan(&name); e != nil {
		return e
	}
	payload, e := json.Marshal(map[string]any{"invitationId": inv.ID, "email": inv.Email, "organizationName": name, "role": inv.Role, "token": token, "expiresAt": inv.ExpiresAt.Format(time.RFC3339)})
	if e != nil {
		return e
	}
	return outbox.Enqueue(ctx, s.tx, outbox.Event{
		OrganizationID: inv.OrganizationID, Type: InvitationEvent, AggregateType: "organization_invitation", AggregateID: inv.ID,
		DedupKey: fmt.Sprintf("invitation/%s/%d", inv.ID, inv.SendCount), ActorID: a.ID, Payload: payload,
	})
}

func (s *store) InvitationsPage(ctx context.Context, org, status string, w pagination.Window) ([]domain.Invitation, error) {
	rows, e := s.tx.Query(ctx, invitationSelect+` WHERE i.organization_id=$1
 AND ($2='' OR CASE WHEN i.status='PENDING' AND i.expires_at<=NOW() THEN 'EXPIRED' ELSE i.status END=$2)
 AND ($3='' OR strpos(LOWER(i.email),LOWER($3))>0)
 AND ($5='' OR (LOWER(i.email),i.id::text)>($4,$5)) ORDER BY LOWER(i.email),i.id::text LIMIT $6`, org, status, w.Query, w.After.Key, w.After.ID, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Invitation{}
	for rows.Next() {
		v, e := scanInvitation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *store) LockInvitation(ctx context.Context, org, id string) (domain.Invitation, error) {
	v, e := scanInvitation(s.tx.QueryRow(ctx, invitationSelect+` WHERE i.organization_id=$1 AND i.id=$2 FOR UPDATE OF i`, org, id))
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrInvitationNotFound
	}
	return v, e
}

func (s *store) ResendInvitation(ctx context.Context, a domain.Actor, org, id, hash string) (domain.Invitation, error) {
	if e := s.scrubInvitationEvents(ctx, org, id); e != nil {
		return domain.Invitation{}, e
	}
	_, e := s.tx.Exec(ctx, `UPDATE organization_invitations SET token_hash=$3,status='PENDING',expires_at=NOW()+`+invitationTTLSQL+`,send_count=send_count+1,last_sent_at=NOW(),row_version=row_version+1,updated_by=$4,updated_at=NOW() WHERE organization_id=$1 AND id=$2`, org, id, hash, a.ID)
	if e != nil {
		if errors.Is(conflict(e), domain.ErrConflict) {
			return domain.Invitation{}, domain.ErrInvitationPending
		}
		return domain.Invitation{}, e
	}
	if e = s.auditMeta(ctx, a.ID, org, "invitation.resent", "invitation", id, map[string]any{}); e != nil {
		return domain.Invitation{}, e
	}
	return scanInvitation(s.tx.QueryRow(ctx, invitationSelect+` WHERE i.id=$1`, id))
}

func (s *store) RevokeInvitation(ctx context.Context, a domain.Actor, org, id string) (domain.Invitation, error) {
	if e := s.scrubInvitationEvents(ctx, org, id); e != nil {
		return domain.Invitation{}, e
	}
	if _, e := s.tx.Exec(ctx, `UPDATE organization_invitations SET status='REVOKED',revoked_at=NOW(),row_version=row_version+1,updated_by=$3,updated_at=NOW() WHERE organization_id=$1 AND id=$2`, org, id, a.ID); e != nil {
		return domain.Invitation{}, e
	}
	if e := s.auditMeta(ctx, a.ID, org, "invitation.revoked", "invitation", id, map[string]any{}); e != nil {
		return domain.Invitation{}, e
	}
	return scanInvitation(s.tx.QueryRow(ctx, invitationSelect+` WHERE i.id=$1`, id))
}

func (s *store) InvitationOrganization(ctx context.Context, hash string) (org string, e error) {
	e = s.tx.QueryRow(ctx, `SELECT organization_id FROM organization_invitations WHERE token_hash=$1`, hash).Scan(&org)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrInvitationNotFound
	}
	return
}

func (s *store) LockInvitationByHash(ctx context.Context, hash string) (domain.Invitation, error) {
	v, e := scanInvitation(s.tx.QueryRow(ctx, invitationSelect+` WHERE i.token_hash=$1 FOR UPDATE OF i`, hash))
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrInvitationNotFound
	}
	return v, e
}

func (s *store) AccountEmail(ctx context.Context, uid string) (email string, e error) {
	e = s.tx.QueryRow(ctx, `SELECT email FROM application_users WHERE id=$1 AND status='ACTIVE'`, uid).Scan(&email)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrDenied
	}
	return
}

func (s *store) AcceptInvitation(ctx context.Context, inv domain.Invitation, uid string) (domain.Accepted, error) {
	out := domain.Accepted{OrganizationID: inv.OrganizationID, Role: inv.Role, LocationIDs: []string{}}
	rows, e := s.tx.Query(ctx, `SELECT id::text FROM locations WHERE organization_id=$1 AND id=ANY($2::uuid[]) AND status='ACTIVE' ORDER BY id`, inv.OrganizationID, inv.LocationIDs)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		out.LocationIDs = append(out.LocationIDs, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	if inv.Role != domain.RoleAdmin && len(out.LocationIDs) == 0 {
		return out, domain.ErrInvitationNotOpen
	}
	var status string
	e = s.tx.QueryRow(ctx, `SELECT status FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 FOR UPDATE`, inv.OrganizationID, uid).Scan(&status)
	switch {
	case errors.Is(e, pgx.ErrNoRows):
		if _, e = s.tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,created_by,updated_by) VALUES($1,$2,$3,$2,$2)`, inv.OrganizationID, uid, inv.Role); e != nil {
			return out, conflict(e)
		}
	case e != nil:
		return out, e
	case status == "ACTIVE":
		return out, domain.ErrMemberExists
	default:
		if _, e = s.tx.Exec(ctx, `UPDATE organization_memberships SET role=$3,status='ACTIVE',row_version=row_version+1,updated_by=$2,updated_at=NOW() WHERE organization_id=$1 AND user_id=$2`, inv.OrganizationID, uid, inv.Role); e != nil {
			return out, e
		}
		if _, e = s.tx.Exec(ctx, `DELETE FROM membership_locations WHERE organization_id=$1 AND user_id=$2`, inv.OrganizationID, uid); e != nil {
			return out, e
		}
	}
	for _, id := range out.LocationIDs {
		if _, e = s.tx.Exec(ctx, `INSERT INTO membership_locations(organization_id,user_id,location_id,created_by,updated_by) VALUES($1,$2,$3,$2,$2)`, inv.OrganizationID, uid, id); e != nil {
			return out, e
		}
	}
	if _, e = s.tx.Exec(ctx, `UPDATE organization_invitations SET status='ACCEPTED',accepted_by=$3,accepted_at=NOW(),row_version=row_version+1,updated_by=$3,updated_at=NOW() WHERE organization_id=$1 AND id=$2`, inv.OrganizationID, inv.ID, uid); e != nil {
		return out, e
	}
	if e = s.scrubInvitationEvents(ctx, inv.OrganizationID, inv.ID); e != nil {
		return out, e
	}
	if e = s.auditMeta(ctx, uid, inv.OrganizationID, "invitation.accepted", "invitation", inv.ID, map[string]any{"role": inv.Role}); e != nil {
		return out, e
	}
	return out, s.auditMeta(ctx, uid, inv.OrganizationID, "membership.created", "membership", uid, map[string]any{"role": inv.Role, "via": "invitation"})
}

// ---------------------------------------------------------------- dispositivos

func (s *store) DevicesPage(ctx context.Context, uid string, w pagination.Window) ([]domain.Device, error) {
	if w.After.ID != "" {
		if _, e := time.Parse(time.RFC3339Nano, w.After.Key); e != nil {
			return nil, pagination.ErrInvalid
		}
	}
	rows, e := s.tx.Query(ctx, `SELECT d.id::text,d.organization_id,COALESCE(NULLIF(o.trade_name,''),o.legal_name),d.platform,d.status,d.last_seen_at,d.created_at
 FROM mobile_devices d JOIN organizations o ON o.id=d.organization_id
 WHERE d.user_id=$1 AND d.status='ACTIVE' AND ($3='' OR (d.created_at,d.id::text)>(NULLIF($2,'')::timestamptz,$3))
 ORDER BY d.created_at,d.id::text LIMIT $4`, uid, w.After.Key, w.After.ID, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Device{}
	for rows.Next() {
		var v domain.Device
		if e = rows.Scan(&v.ID, &v.OrganizationID, &v.OrganizationName, &v.Platform, &v.Status, &v.LastSeenAt, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *store) RevokeDevice(ctx context.Context, a domain.Actor, id string) (v domain.Device, e error) {
	e = s.tx.QueryRow(ctx, `SELECT d.id::text,d.organization_id,COALESCE(NULLIF(o.trade_name,''),o.legal_name),d.platform,d.status,d.last_seen_at,d.created_at
 FROM mobile_devices d JOIN organizations o ON o.id=d.organization_id WHERE d.id=$1 AND d.user_id=$2 FOR UPDATE OF d`, id, a.ID).
		Scan(&v.ID, &v.OrganizationID, &v.OrganizationName, &v.Platform, &v.Status, &v.LastSeenAt, &v.CreatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, domain.ErrNotFound
	}
	if e != nil || v.Status == "INACTIVE" {
		return v, e
	}
	if _, e = s.tx.Exec(ctx, `UPDATE mobile_devices SET status='INACTIVE',push_token=NULL,token_hash=NULL,updated_by=$2,updated_at=NOW() WHERE id=$1`, id, a.ID); e != nil {
		return v, e
	}
	v.Status = "INACTIVE"
	return v, s.auditMeta(ctx, a.ID, v.OrganizationID, "device.revoked", "device", id, map[string]any{})
}
