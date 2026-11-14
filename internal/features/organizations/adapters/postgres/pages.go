package postgres

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// Las claves usan la misma colación y el mismo desempate en el filtro y el orden.
// strpos trata % y _ como texto literal, sin comodines ni interpolación SQL.
func (s *store) OrganizationsPage(ctx context.Context, a domain.Actor, w pagination.Window) ([]domain.Organization, error) {
	rows, e := s.tx.Query(ctx, `SELECT `+orgColumns+` FROM organizations o
 WHERE o.status='ACTIVE' AND ($2='SUPER_ADMIN' OR EXISTS(SELECT 1 FROM organization_memberships m WHERE m.organization_id=o.id AND m.user_id=$1 AND m.status='ACTIVE'))
 AND ($3='' OR strpos(lower(o.legal_name||' '||COALESCE(o.trade_name,'')),lower($3))>0)
 AND ($5='' OR (o.legal_name,o.id)>($4,$5)) ORDER BY o.legal_name,o.id LIMIT $6`, a.ID, a.Role, w.Query, w.After.Key, w.After.ID, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Organization{}
	for rows.Next() {
		var o domain.Organization
		if e = rows.Scan(&o.ID, &o.LegalName, &o.TradeName, &o.TaxID, &o.BusinessType, &o.ContactName, &o.ContactPhone, &o.ContactEmail); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *store) LocationsPage(ctx context.Context, a domain.Actor, org, role string, w pagination.Window) ([]domain.Location, error) {
	rows, e := s.tx.Query(ctx, `SELECT l.id::text,l.name,l.address,COALESCE(l.district,''),COALESCE(l.contact_phone,''),l.timezone FROM locations l
 WHERE l.organization_id=$1 AND l.status='ACTIVE' AND ($3 OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=$1 AND ml.user_id=$2 AND ml.location_id=l.id))
 AND ($4='' OR strpos(lower(l.name||' '||l.address||' '||COALESCE(l.district,'')),lower($4))>0)
 AND ($6='' OR (l.name,l.id::text)>($5,$6)) ORDER BY l.name,l.id::text LIMIT $7`, org, a.ID, domain.CanManage(role), w.Query, w.After.Key, w.After.ID, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Location{}
	for rows.Next() {
		var v domain.Location
		if e = rows.Scan(&v.ID, &v.Name, &v.Address, &v.District, &v.ContactPhone, &v.Timezone); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *store) MembersPage(ctx context.Context, org string, w pagination.Window) ([]domain.Member, error) {
	rows, e := s.tx.Query(ctx, `SELECT u.id,u.display_name,u.email,m.role,COALESCE((SELECT array_agg(ml.location_id::text ORDER BY ml.location_id) FROM membership_locations ml WHERE ml.organization_id=$1 AND ml.user_id=u.id),'{}'::text[]) FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE'
 WHERE m.organization_id=$1 AND m.status='ACTIVE'
 AND ($2='' OR strpos(lower(u.display_name||' '||u.email),lower($2))>0)
 AND ($4='' OR (u.display_name,u.id)>($3,$4)) ORDER BY u.display_name,u.id LIMIT $5`, org, w.Query, w.After.Key, w.After.ID, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Member{}
	for rows.Next() {
		var v domain.Member
		if e = rows.Scan(&v.ID, &v.Name, &v.Email, &v.Role, &v.LocationIDs); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *store) EntriesPage(ctx context.Context, a domain.Actor, org, role, resource string, w pagination.Window) ([]domain.Entry, error) {
	switch resource {
	case "courses":
		return s.entries(ctx, `SELECT c.id::text,c.title,LEFT(COALESCE(c.description,''),500),COALESCE(p.status,'NOT_STARTED'),c.title FROM training_courses c LEFT JOIN training_progress p ON p.organization_id=c.organization_id AND p.course_id=c.id AND p.user_id=$2 WHERE c.organization_id=$1 AND c.status='PUBLISHED' AND ($3='' OR strpos(lower(c.title||' '||COALESCE(c.description,'')),lower($3))>0) AND ($5='' OR (c.title,c.id::text)>($4,$5)) ORDER BY c.title,c.id::text LIMIT $6`, org, a.ID, w.Query, w.After.Key, w.After.ID, w.Size+1)
	case "checklist-summaries":
		return s.entries(ctx, `SELECT id::text,name,LEFT(COALESCE(description,''),500),status,name FROM checklist_templates WHERE organization_id=$1 AND status='ACTIVE' AND ($2='' OR strpos(lower(name||' '||COALESCE(description,'')),lower($2))>0) AND ($4='' OR (name,id::text)>($3,$4)) ORDER BY name,id::text LIMIT $5`, org, w.Query, w.After.Key, w.After.ID, w.Size+1)
	case "incidents":
		if w.After.ID != "" {
			if _, e := time.Parse(time.RFC3339Nano, w.After.Key); e != nil {
				return nil, pagination.ErrInvalid
			}
		}
		return s.entries(ctx, `SELECT i.id::text,c.name,LEFT(COALESCE(i.description,''),500),i.status,to_char(i.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') FROM incidents i JOIN incident_catalog c ON c.organization_id=i.organization_id AND c.id=i.catalog_id JOIN locations l ON l.organization_id=i.organization_id AND l.id=i.location_id AND l.status='ACTIVE'
 WHERE i.organization_id=$1 AND ($3 OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=$1 AND ml.user_id=$2 AND ml.location_id=i.location_id)) AND ($4<>'WORKER' OR i.reported_by=$2)
 AND ($5='' OR strpos(lower(c.name||' '||COALESCE(i.description,'')),lower($5))>0) AND ($7='' OR i.created_at<NULLIF($6,'')::timestamptz OR (i.created_at=NULLIF($6,'')::timestamptz AND i.id::text>$7)) ORDER BY i.created_at DESC,i.id::text LIMIT $8`, org, a.ID, domain.CanManage(role), role, w.Query, w.After.Key, w.After.ID, w.Size+1)
	}
	return nil, pagination.ErrInvalid
}

func (s *store) WorkspacePage(ctx context.Context, a domain.Actor, org, role string, size int) (out domain.Workspace, err error) {
	out.Role = role
	out.NextTokens = map[string]string{}
	out.Totals = map[string]int{}
	out.Members = []domain.Member{}
	err = s.tx.QueryRow(ctx, `SELECT `+orgColumns+` FROM organizations o WHERE o.id=$1`, org).Scan(&out.Organization.ID, &out.Organization.LegalName, &out.Organization.TradeName, &out.Organization.TaxID, &out.Organization.BusinessType, &out.Organization.ContactName, &out.Organization.ContactPhone, &out.Organization.ContactEmail)
	if err != nil {
		return
	}
	w := pagination.Window{Size: size}
	ls, e := s.LocationsPage(ctx, a, org, role, w)
	if e != nil {
		return out, e
	}
	lp := pagination.Close(ls, w, func(v domain.Location) pagination.Position { return pagination.Position{Key: v.Name, ID: v.ID} }, a.ID, org, "locations")
	out.Locations = lp.Items
	out.NextTokens["locations"] = lp.NextToken
	if domain.CanManage(role) {
		ms, e := s.MembersPage(ctx, org, w)
		if e != nil {
			return out, e
		}
		mp := pagination.Close(ms, w, func(v domain.Member) pagination.Position { return pagination.Position{Key: v.Name, ID: v.ID} }, a.ID, org, "members")
		out.Members = mp.Items
		out.NextTokens["members"] = mp.NextToken
	}
	for _, resource := range []string{"courses", "checklist-summaries", "incidents"} {
		es, e := s.EntriesPage(ctx, a, org, role, resource, w)
		if e != nil {
			return out, e
		}
		ep := pagination.Close(es, w, func(v domain.Entry) pagination.Position {
			return pagination.Position{Key: v.CursorKey, ID: v.ID}
		}, a.ID, org, resource)
		out.NextTokens[resource] = ep.NextToken
		switch resource {
		case "courses":
			out.Courses = ep.Items
		case "checklist-summaries":
			out.Checklists = ep.Items
		case "incidents":
			out.Incidents = ep.Items
		}
	}
	// Los indicadores cuentan todo el conjunto autorizado, nunca solo una página de sedes.
	var locations, courses, checklists int
	err = s.tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM locations l WHERE l.organization_id=$1 AND l.status='ACTIVE' AND ($3 OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=$1 AND ml.user_id=$2 AND ml.location_id=l.id))),
 (SELECT count(*) FROM training_courses WHERE organization_id=$1 AND status='PUBLISHED'),
 (SELECT count(*) FROM checklist_templates t WHERE t.organization_id=$1 AND t.status='ACTIVE' AND EXISTS(SELECT 1 FROM checklist_template_versions v WHERE v.organization_id=t.organization_id AND v.template_id=t.id AND v.status='PUBLISHED'))`, org, a.ID, domain.CanManage(role)).Scan(&locations, &courses, &checklists)
	out.Totals = map[string]int{"locations": locations, "courses": courses, "checklists": checklists}
	return
}
