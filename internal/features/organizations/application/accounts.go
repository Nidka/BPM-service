package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // zonas horarias disponibles aunque el sistema operativo no las incluya

	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	inputformat "github.com/Nidka/BPM-service/internal/shared/domain/input"
)

// AccountStore reúne las primitivas transaccionales de gestión de cuentas.
// Todas se ejecutan dentro de la misma transacción que la autorización.
type AccountStore interface {
	// LockForManagement autoriza a un ADMIN/SUPER_ADMIN y bloquea la organización
	// para serializar los cambios de cuentas; devuelve el rol efectivo.
	LockForManagement(context.Context, domain.Actor, string) (string, error)
	// LockOrganizationRow bloquea una organización activa sin autorizar (aceptación).
	LockOrganizationRow(context.Context, string) error
	LockOrganization(context.Context, string) (domain.Organization, error)
	SaveOrganization(context.Context, domain.Actor, domain.Organization, []string) (domain.Organization, error)
	LockLocation(context.Context, string, string) (domain.Location, error)
	SaveLocation(context.Context, domain.Actor, string, domain.Location, []string) (domain.Location, error)
	LockMember(context.Context, string, string) (domain.Member, error)
	CountOtherActiveAdmins(context.Context, string, string) (int, error)
	CheckLocations(context.Context, string, []string) error
	CountMemberLocations(context.Context, string, string) (int, error)
	SaveMember(context.Context, domain.Actor, string, string, domain.MemberChange) (domain.Member, error)
	MembersPageByStatus(context.Context, string, string, pagination.Window) ([]domain.Member, error)
	LocationsPageByStatus(context.Context, domain.Actor, string, string, string, pagination.Window) ([]domain.Location, error)
	CreateInvitation(context.Context, domain.Actor, string, domain.Invitation) (domain.Invitation, bool, error)
	QueueInvitationEmail(context.Context, domain.Actor, domain.Invitation, string) error
	InvitationsPage(context.Context, string, string, pagination.Window) ([]domain.Invitation, error)
	LockInvitation(context.Context, string, string) (domain.Invitation, error)
	ResendInvitation(context.Context, domain.Actor, string, string, string) (domain.Invitation, error)
	RevokeInvitation(context.Context, domain.Actor, string, string) (domain.Invitation, error)
	InvitationOrganization(context.Context, string) (string, error)
	LockInvitationByHash(context.Context, string) (domain.Invitation, error)
	AccountEmail(context.Context, string) (string, error)
	AcceptInvitation(context.Context, domain.Invitation, string) (domain.Accepted, error)
	DevicesPage(context.Context, string, pagination.Window) ([]domain.Device, error)
	RevokeDevice(context.Context, domain.Actor, string) (domain.Device, error)
}

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// NewInvitationToken genera 256 bits aleatorios y su hash SHA-256. Solo el hash se guarda.
func NewInvitationToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashInvitationToken(token), nil
}

// HashInvitationToken devuelve el SHA-256 hexadecimal del token.
func HashInvitationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validateOrganization(fields map[string]string, input *domain.Organization) {
	validateText(fields, "legalName", input.LegalName, 2, 200)
	validateText(fields, "tradeName", input.TradeName, 0, 200)
	validateText(fields, "taxId", input.TaxID, 0, 20)
	if !inputformat.PersonName(input.ContactName, 0, 160) {
		fields["contactName"] = "Usa letras, espacios, apóstrofos o guiones; sin números (hasta 160 caracteres)."
	}
	if !inputformat.Phone(input.ContactPhone) {
		fields["contactPhone"] = "Usa entre 7 y 15 dígitos; puedes incluir + al inicio, espacios, guiones y paréntesis."
	}
	if !inputformat.Text(input.ContactEmail, 0, 320) || len(input.ContactEmail) > 320 ||
		strings.TrimSpace(input.ContactEmail) != "" && !inputformat.Email(input.ContactEmail) {
		fields["contactEmail"] = "Ingresa un correo válido."
	}
	// RELATED_SERVICE es el nombre anterior de OTHER_FOOD_SERVICE; se acepta solo como entrada.
	if input.BusinessType == "RELATED_SERVICE" {
		input.BusinessType = "OTHER_FOOD_SERVICE"
	}
	if !domain.ValidBusinessType(input.BusinessType) {
		fields["businessType"] = "Selecciona un tipo de establecimiento."
	}
}

func validLocations(fields map[string]string, role string, ids []string) []string {
	if len(ids) > 100 {
		fields["locationIds"] = "Selecciona como máximo 100 sedes."
		return ids
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !uuid.MatchString(id) {
			fields["locationIds"] = "Selecciona sedes válidas."
			return ids
		}
		id = strings.ToLower(id)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if role != domain.RoleAdmin && len(out) == 0 {
		fields["locationIds"] = "Asigna al menos una sede."
	}
	return out
}

func validRole(role string) bool {
	return role == domain.RoleAdmin || role == domain.RoleChef || role == domain.RoleWorker
}

// manage autoriza al gestor y bloquea la organización antes de ejecutar la operación.
func (s *Service) manage(ctx context.Context, uid, org string, fn func(Store, domain.Actor) error) error {
	return s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		if _, e = db.LockForManagement(ctx, actor, org); e != nil {
			return e
		}
		return fn(db, actor)
	})
}

// ---------------------------------------------------------------- organización y sedes

// UpdateOrganization edita los datos y el tipo de establecimiento. Es un reemplazo
// completo de los campos editables, protegido por rowVersion.
func (s *Service) UpdateOrganization(ctx context.Context, uid, org string, input domain.Organization, version int) (out domain.Organization, err error) {
	fields := map[string]string{}
	validateOrganization(fields, &input)
	if version < 1 {
		fields["rowVersion"] = "Indica la versión que estás editando."
	}
	if len(fields) > 0 {
		return out, &domain.ValidationError{Fields: fields}
	}
	input.ID = org
	input.LegalName = strings.TrimSpace(input.LegalName)
	input.TradeName = strings.TrimSpace(input.TradeName)
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		cur, e := db.LockOrganization(ctx, org)
		if e != nil {
			return e
		}
		if cur.RowVersion != version {
			return domain.ErrVersionConflict
		}
		changed := []string{}
		for _, f := range []struct{ name, a, b string }{
			{"legalName", cur.LegalName, input.LegalName}, {"tradeName", cur.TradeName, input.TradeName},
			{"taxId", cur.TaxID, input.TaxID}, {"businessType", cur.BusinessType, input.BusinessType},
			{"contactName", cur.ContactName, input.ContactName}, {"contactPhone", cur.ContactPhone, input.ContactPhone},
			{"contactEmail", cur.ContactEmail, input.ContactEmail},
		} {
			if f.a != f.b {
				changed = append(changed, f.name)
			}
		}
		if len(changed) == 0 {
			out = cur
			return nil
		}
		out, e = db.SaveOrganization(ctx, actor, input, changed)
		return e
	})
	return
}

func validateLocation(fields map[string]string, input *domain.Location) {
	validateText(fields, "name", input.Name, 2, 160)
	validateText(fields, "address", input.Address, 3, 300)
	validateText(fields, "district", input.District, 0, 120)
	if !inputformat.Phone(input.ContactPhone) {
		fields["contactPhone"] = "Usa entre 7 y 15 dígitos; puedes incluir + al inicio, espacios, guiones y paréntesis."
	}
	if _, e := time.LoadLocation(input.Timezone); e != nil || input.Timezone == "" || input.Timezone == "Local" || len(input.Timezone) > 80 {
		fields["timezone"] = "Indica una zona horaria IANA válida, por ejemplo America/Lima."
	}
	if input.Status != "ACTIVE" && input.Status != "INACTIVE" {
		fields["status"] = "Usa ACTIVE o INACTIVE."
	}
}

// UpdateLocation edita una sede (incluida su zona horaria y su estado activo/inactivo).
func (s *Service) UpdateLocation(ctx context.Context, uid, org, id string, input domain.Location, version int) (out domain.Location, err error) {
	fields := map[string]string{}
	if !uuid.MatchString(id) {
		return out, domain.ErrNotFound
	}
	validateLocation(fields, &input)
	if version < 1 {
		fields["rowVersion"] = "Indica la versión que estás editando."
	}
	if len(fields) > 0 {
		return out, &domain.ValidationError{Fields: fields}
	}
	input.ID = strings.ToLower(id)
	input.Name = strings.TrimSpace(input.Name)
	input.Address = strings.TrimSpace(input.Address)
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		cur, e := db.LockLocation(ctx, org, input.ID)
		if e != nil {
			return e
		}
		if cur.RowVersion != version {
			return domain.ErrVersionConflict
		}
		changed := []string{}
		for _, f := range []struct{ name, a, b string }{
			{"name", cur.Name, input.Name}, {"address", cur.Address, input.Address}, {"district", cur.District, input.District},
			{"contactPhone", cur.ContactPhone, input.ContactPhone}, {"timezone", cur.Timezone, input.Timezone}, {"status", cur.Status, input.Status},
		} {
			if f.a != f.b {
				changed = append(changed, f.name)
			}
		}
		if len(changed) == 0 {
			out = cur
			return nil
		}
		out, e = db.SaveLocation(ctx, actor, org, input, changed)
		return e
	})
	return
}

// ---------------------------------------------------------------- miembros

// MemberUpdate es la edición de rol y sedes de una membresía.
type MemberUpdate struct {
	Role        string
	LocationIDs []string
	RowVersion  int
}

// UpdateMember cambia el rol y las sedes. Un ADMIN no puede dejar la empresa sin ADMIN activo.
func (s *Service) UpdateMember(ctx context.Context, uid, org, target string, in MemberUpdate) (out domain.Member, err error) {
	fields := map[string]string{}
	if !validRole(in.Role) {
		fields["role"] = "Selecciona un rol válido."
	}
	locations := validLocations(fields, in.Role, in.LocationIDs)
	if in.RowVersion < 1 {
		fields["rowVersion"] = "Indica la versión que estás editando."
	}
	if len(fields) > 0 {
		return out, &domain.ValidationError{Fields: fields}
	}
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		cur, e := db.LockMember(ctx, org, target)
		if e != nil {
			return e
		}
		if cur.RowVersion != in.RowVersion {
			return domain.ErrVersionConflict
		}
		if e = db.CheckLocations(ctx, org, locations); e != nil {
			return e
		}
		if cur.Status == "ACTIVE" && cur.Role == domain.RoleAdmin && in.Role != domain.RoleAdmin {
			if e = requireOtherAdmin(ctx, db, org, target); e != nil {
				return e
			}
		}
		fieldsChanged := []string{}
		if cur.Role != in.Role {
			fieldsChanged = append(fieldsChanged, "role")
		}
		current := slices.Clone(cur.LocationIDs)
		slices.Sort(current)
		wanted := slices.Clone(locations)
		slices.Sort(wanted)
		if !slices.Equal(current, wanted) {
			fieldsChanged = append(fieldsChanged, "locationIds")
		}
		if len(fieldsChanged) == 0 {
			out = cur
			return nil
		}
		out, e = db.SaveMember(ctx, actor, org, target, domain.MemberChange{Role: in.Role, Status: cur.Status, Locations: &locations, Action: "membership.updated", Fields: fieldsChanged})
		return e
	})
	return
}

// requireOtherAdmin comprueba que, sin el miembro objetivo, queda al menos un ADMIN activo.
func requireOtherAdmin(ctx context.Context, db Store, org, target string) error {
	n, e := db.CountOtherActiveAdmins(ctx, org, target)
	if e != nil {
		return e
	}
	if n < 1 {
		return domain.ErrLastAdmin
	}
	return nil
}

// DeactivateMember da de baja lógica la membresía, retira sus dispositivos y revoca sesiones.
func (s *Service) DeactivateMember(ctx context.Context, uid, org, target string, version int) (out domain.Member, err error) {
	if version < 1 {
		return out, &domain.ValidationError{Fields: map[string]string{"rowVersion": "Indica la versión que estás editando."}}
	}
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		cur, e := db.LockMember(ctx, org, target)
		if e != nil {
			return e
		}
		if cur.Status == "INACTIVE" {
			out = cur
			return nil
		}
		if cur.RowVersion != version {
			return domain.ErrVersionConflict
		}
		if cur.Role == domain.RoleAdmin {
			if e = requireOtherAdmin(ctx, db, org, target); e != nil {
				return e
			}
		}
		out, e = db.SaveMember(ctx, actor, org, target, domain.MemberChange{Role: cur.Role, Status: "INACTIVE", Action: "membership.deactivated", Fields: []string{"status"}})
		return e
	})
	return
}

// ReactivateMember restablece una membresía dada de baja con su rol y sedes previos.
func (s *Service) ReactivateMember(ctx context.Context, uid, org, target string, version int) (out domain.Member, err error) {
	if version < 1 {
		return out, &domain.ValidationError{Fields: map[string]string{"rowVersion": "Indica la versión que estás editando."}}
	}
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		cur, e := db.LockMember(ctx, org, target)
		if e != nil {
			return e
		}
		if cur.Status == "ACTIVE" {
			out = cur
			return nil
		}
		if cur.RowVersion != version {
			return domain.ErrVersionConflict
		}
		if !cur.AccountActive {
			return domain.ErrMemberUnavailable
		}
		if cur.Role != domain.RoleAdmin {
			n, e := db.CountMemberLocations(ctx, org, target)
			if e != nil {
				return e
			}
			if n == 0 {
				return &domain.ValidationError{Fields: map[string]string{"locationIds": "Asigna al menos una sede activa antes de reactivar."}}
			}
		}
		out, e = db.SaveMember(ctx, actor, org, target, domain.MemberChange{Role: cur.Role, Status: "ACTIVE", Action: "membership.reactivated", Fields: []string{"status"}})
		return e
	})
	return
}

// TransferInput describe la transferencia de la administración.
type TransferInput struct {
	// RowVersion es la versión de la membresía destino.
	RowVersion int
	// ActorRole y ActorLocationIDs describen en qué se convierte quien transfiere
	// (vacíos si quien transfiere es un SUPER_ADMIN sin membresía).
	ActorRole        string
	ActorLocationIDs []string
}

// TransferAdmin convierte al destino en ADMIN y degrada a quien transfiere, de forma atómica.
func (s *Service) TransferAdmin(ctx context.Context, uid, org, target string, in TransferInput) (newAdmin, former domain.Member, err error) {
	fields := map[string]string{}
	if in.RowVersion < 1 {
		fields["rowVersion"] = "Indica la versión que estás editando."
	}
	if in.ActorRole != "" && in.ActorRole != domain.RoleChef && in.ActorRole != domain.RoleWorker {
		fields["actorRole"] = "Indica CHEF o WORKER para quien transfiere."
	}
	locations := []string{}
	if in.ActorRole != "" {
		sub := map[string]string{}
		locations = validLocations(sub, in.ActorRole, in.ActorLocationIDs)
		if sub["locationIds"] != "" {
			fields["actorLocationIds"] = sub["locationIds"]
		}
	}
	if uid == target {
		fields["userId"] = "Elige a otra persona como nuevo administrador."
	}
	if len(fields) > 0 {
		return newAdmin, former, &domain.ValidationError{Fields: fields}
	}
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		dest, e := db.LockMember(ctx, org, target)
		if e != nil {
			return e
		}
		self, e := db.LockMember(ctx, org, uid)
		isMember := e == nil
		if e != nil && !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		selfAdmin := isMember && self.Status == "ACTIVE" && self.Role == domain.RoleAdmin
		if selfAdmin && in.ActorRole == "" {
			return &domain.ValidationError{Fields: map[string]string{"actorRole": "Indica en qué rol queda quien transfiere."}}
		}
		if !selfAdmin && in.ActorRole != "" {
			return &domain.ValidationError{Fields: map[string]string{"actorRole": "Quien transfiere no es administrador de esta organización."}}
		}
		if dest.RowVersion != in.RowVersion {
			return domain.ErrVersionConflict
		}
		if dest.Status != "ACTIVE" || !dest.AccountActive {
			return domain.ErrMemberInactive
		}
		if e = db.CheckLocations(ctx, org, locations); e != nil {
			return e
		}
		newAdmin, e = db.SaveMember(ctx, actor, org, target, domain.MemberChange{Role: domain.RoleAdmin, Status: "ACTIVE", Action: "membership.admin_transferred_in", Fields: []string{"role"}})
		if e != nil {
			return e
		}
		if selfAdmin {
			former, e = db.SaveMember(ctx, actor, org, uid, domain.MemberChange{Role: in.ActorRole, Status: "ACTIVE", Locations: &locations, Action: "membership.admin_transferred_out", Fields: []string{"role", "locationIds"}})
		}
		return e
	})
	return
}

func pageScope(uid, org, resource, status string) []string {
	return []string{uid, org, resource, status}
}

// MembersByStatus lista miembros ACTIVE (predeterminado) o INACTIVE; solo ADMIN/SUPER_ADMIN.
func (s *Service) MembersByStatus(ctx context.Context, uid, org, status string, r pagination.Request) (out pagination.Page[domain.Member], err error) {
	if status == "" {
		status = "ACTIVE"
	}
	if status != "ACTIVE" && status != "INACTIVE" {
		return out, pagination.ErrInvalid
	}
	scope := pageScope(uid, org, "members", status)
	w, err := pagination.Open(r, scope...)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		role, e := db.Role(ctx, a, org)
		if e != nil {
			return e
		}
		if !domain.CanManage(role) {
			return domain.ErrDenied
		}
		items, e := db.MembersPageByStatus(ctx, org, status, w)
		if e != nil {
			return e
		}
		out = pagination.Close(items, w, func(v domain.Member) pagination.Position { return pagination.Position{Key: v.Name, ID: v.ID} }, scope...)
		return nil
	})
	return
}

// LocationsByStatus lista sedes ACTIVE (predeterminado) o INACTIVE; INACTIVE exige ADMIN.
func (s *Service) LocationsByStatus(ctx context.Context, uid, org, status string, r pagination.Request) (out pagination.Page[domain.Location], err error) {
	if status == "" {
		status = "ACTIVE"
	}
	if status != "ACTIVE" && status != "INACTIVE" {
		return out, pagination.ErrInvalid
	}
	scope := pageScope(uid, org, "locations", status)
	w, err := pagination.Open(r, scope...)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		role, e := db.Role(ctx, a, org)
		if e != nil {
			return e
		}
		if status == "INACTIVE" && !domain.CanManage(role) {
			return domain.ErrDenied
		}
		items, e := db.LocationsPageByStatus(ctx, a, org, role, status, w)
		if e != nil {
			return e
		}
		out = pagination.Close(items, w, func(v domain.Location) pagination.Position { return pagination.Position{Key: v.Name, ID: v.ID} }, scope...)
		return nil
	})
	return
}

// ---------------------------------------------------------------- invitaciones

// InvitationInput es la solicitud de invitación. ID es un UUID del cliente para reintentos.
type InvitationInput struct {
	ID, Email, Role string
	LocationIDs     []string
}

// InvitationShare es el material para compartir una invitación recién emitida.
// Solo existe en la respuesta de crear y reenviar; nunca se guarda ni se registra.
type InvitationShare struct {
	// Issued es false cuando no hay token (repetición idempotente).
	Issued bool
	// AcceptURL es la URL con el token; vacía si INVITATION_ACCEPT_URL no está configurada.
	AcceptURL string
	// Token solo se rellena sin AcceptURL, para pegarlo en la app.
	Token string
}

// InvitationLink construye la URL de aceptación con el token como parámetro.
// Devuelve "" si base está vacía o no es una URL válida.
func InvitationLink(base, token string) string {
	u, e := url.Parse(base)
	if base == "" || e != nil {
		return ""
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Service) share(token string) InvitationShare {
	if link := InvitationLink(s.acceptURL, token); link != "" {
		return InvitationShare{Issued: true, AcceptURL: link}
	}
	return InvitationShare{Issued: true, Token: token}
}

// Invite crea la invitación; ver InviteShared.
func (s *Service) Invite(ctx context.Context, uid, org string, in InvitationInput) (domain.Invitation, bool, error) {
	out, _, created, err := s.InviteShared(ctx, uid, org, in)
	return out, created, err
}

// InviteShared crea la invitación y, si hay proveedor de correo, encola el aviso en la
// misma transacción. created es false cuando se repite una solicitud idéntica ya
// registrada; en ese caso no hay token que compartir (solo se guarda su hash).
func (s *Service) InviteShared(ctx context.Context, uid, org string, in InvitationInput) (out domain.Invitation, share InvitationShare, created bool, err error) {
	fields := map[string]string{}
	if !uuid.MatchString(in.ID) {
		fields["id"] = "Identificador de operación inválido."
	}
	if !inputformat.Email(in.Email) {
		fields["email"] = "Ingresa un correo válido."
	}
	if !validRole(in.Role) {
		fields["role"] = "Selecciona un rol válido."
	}
	locations := validLocations(fields, in.Role, in.LocationIDs)
	if len(fields) > 0 {
		return out, share, false, &domain.ValidationError{Fields: fields}
	}
	token, hash, err := NewInvitationToken()
	if err != nil {
		return out, share, false, err
	}
	inv := domain.Invitation{ID: strings.ToLower(in.ID), Email: strings.ToLower(strings.TrimSpace(in.Email)), Role: in.Role, LocationIDs: locations, TokenHash: hash}
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		if e := db.CheckLocations(ctx, org, locations); e != nil {
			return e
		}
		var e error
		out, created, e = db.CreateInvitation(ctx, actor, org, inv)
		if e != nil || !created {
			return e
		}
		share = s.share(token)
		if !s.queueMail {
			return nil
		}
		return db.QueueInvitationEmail(ctx, actor, out, token)
	})
	return
}

// InvitationsPage lista invitaciones; status vacío devuelve todas.
func (s *Service) InvitationsPage(ctx context.Context, uid, org, status string, r pagination.Request) (out pagination.Page[domain.Invitation], err error) {
	switch status {
	case "", domain.InvitationPending, domain.InvitationAccepted, domain.InvitationRevoked, domain.InvitationExpired:
	default:
		return out, pagination.ErrInvalid
	}
	scope := pageScope(uid, org, "invitations", status)
	w, err := pagination.Open(r, scope...)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		role, e := db.Role(ctx, a, org)
		if e != nil {
			return e
		}
		if !domain.CanManage(role) {
			return domain.ErrDenied
		}
		items, e := db.InvitationsPage(ctx, org, status, w)
		if e != nil {
			return e
		}
		out = pagination.Close(items, w, func(v domain.Invitation) pagination.Position { return pagination.Position{Key: v.Email, ID: v.ID} }, scope...)
		return nil
	})
	return
}

// ResendInvitation reenvía la invitación; ver ResendShared.
func (s *Service) ResendInvitation(ctx context.Context, uid, org, id string) (domain.Invitation, error) {
	out, _, err := s.ResendShared(ctx, uid, org, id)
	return out, err
}

// ResendShared emite un token nuevo (invalida el anterior) y, si hay proveedor de
// correo, encola otro aviso. Devuelve el material para compartir el enlace.
func (s *Service) ResendShared(ctx context.Context, uid, org, id string) (out domain.Invitation, share InvitationShare, err error) {
	if !uuid.MatchString(id) {
		return out, share, domain.ErrInvitationNotFound
	}
	token, hash, err := NewInvitationToken()
	if err != nil {
		return out, share, err
	}
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		cur, e := db.LockInvitation(ctx, org, strings.ToLower(id))
		if e != nil {
			return e
		}
		if cur.Status == domain.InvitationAccepted || cur.Status == domain.InvitationRevoked {
			return domain.ErrInvitationNotOpen
		}
		if time.Since(cur.LastSentAt) < domain.ResendCooldown {
			return domain.ErrResendTooSoon
		}
		out, e = db.ResendInvitation(ctx, actor, org, cur.ID, hash)
		if e != nil {
			return e
		}
		share = s.share(token)
		if !s.queueMail {
			return nil
		}
		return db.QueueInvitationEmail(ctx, actor, out, token)
	})
	return
}

// RevokeInvitation cancela una invitación pendiente o vencida.
func (s *Service) RevokeInvitation(ctx context.Context, uid, org, id string) (out domain.Invitation, err error) {
	if !uuid.MatchString(id) {
		return out, domain.ErrInvitationNotFound
	}
	err = s.manage(ctx, uid, org, func(db Store, actor domain.Actor) error {
		cur, e := db.LockInvitation(ctx, org, strings.ToLower(id))
		if e != nil {
			return e
		}
		if cur.Status == domain.InvitationRevoked {
			out = cur
			return nil
		}
		if cur.Status == domain.InvitationAccepted {
			return domain.ErrInvitationNotOpen
		}
		out, e = db.RevokeInvitation(ctx, actor, org, cur.ID)
		return e
	})
	return
}

// AcceptInvitation une al usuario autenticado a la organización. tokenEmail
// proviene del token Firebase verificado (correo verificado), nunca del cliente.
func (s *Service) AcceptInvitation(ctx context.Context, uid, tokenEmail, token string) (out domain.Accepted, err error) {
	if !tokenPattern.MatchString(token) {
		return out, domain.ErrInvitationNotFound
	}
	hash := HashInvitationToken(token)
	err = s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		org, e := db.InvitationOrganization(ctx, hash)
		if e != nil {
			return e
		}
		// Orden de bloqueo fijo: organización y después invitación.
		if e = db.LockOrganizationRow(ctx, org); e != nil {
			return domain.ErrInvitationNotFound
		}
		inv, e := db.LockInvitationByHash(ctx, hash)
		if e != nil {
			return e
		}
		switch inv.Status {
		case domain.InvitationAccepted:
			return domain.ErrInvitationUsed
		case domain.InvitationRevoked:
			return domain.ErrInvitationRevoked
		case domain.InvitationExpired:
			return domain.ErrInvitationExpired
		}
		profileEmail, e := db.AccountEmail(ctx, actor.ID)
		if e != nil {
			return e
		}
		if !strings.EqualFold(strings.TrimSpace(tokenEmail), inv.Email) || !strings.EqualFold(profileEmail, inv.Email) {
			return domain.ErrInvitationEmail
		}
		out, e = db.AcceptInvitation(ctx, inv, actor.ID)
		return e
	})
	return
}

// ---------------------------------------------------------------- dispositivos

// MyDevices lista los dispositivos activos del propio usuario, nunca los de otros.
func (s *Service) MyDevices(ctx context.Context, uid string, r pagination.Request) (out pagination.Page[domain.Device], err error) {
	scope := []string{uid, "devices"}
	w, err := pagination.Open(r, scope...)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		items, e := db.DevicesPage(ctx, a.ID, w)
		if e != nil {
			return e
		}
		out = pagination.Close(items, w, func(v domain.Device) pagination.Position {
			return pagination.Position{Key: v.CreatedAt.UTC().Format(time.RFC3339Nano), ID: v.ID}
		}, scope...)
		return nil
	})
	return
}

// RevokeMyDevice desactiva un dispositivo propio y retira su token. Es idempotente.
func (s *Service) RevokeMyDevice(ctx context.Context, uid, id string) (out domain.Device, err error) {
	if !uuid.MatchString(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, uid)
		if e != nil {
			return e
		}
		out, e = db.RevokeDevice(ctx, a, strings.ToLower(id))
		return e
	})
	return
}
