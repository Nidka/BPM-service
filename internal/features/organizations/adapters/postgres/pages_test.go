package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	organizationhttp "github.com/Nidka/BPM-service/internal/features/organizations/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)

func testPagination(t *testing.T, ctx context.Context, pool *pgxpool.Pool, service *application.Service, org, other string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`UPDATE application_users SET status='ACTIVE' WHERE id='root'`)
	exec(`UPDATE organization_memberships SET status='ACTIVE' WHERE organization_id=$1 AND user_id='worker'`, org)
	exec(`INSERT INTO organizations(id,legal_name,business_type) SELECT 'page-org-'||n,'Mismo nombre','RESTAURANT' FROM generate_series(1,137) n`)
	exec(`INSERT INTO locations(id,organization_id,name,address) SELECT md5('location-'||n)::uuid,$1,'Sede '||lpad(n::text,3,'0'),'Av. Prueba' FROM generate_series(1,137) n`, org)
	exec(`INSERT INTO application_users(id,email,display_name) SELECT 'page-user-'||n,'page-'||n||'@example.invalid','Mismo nombre' FROM generate_series(1,137)n`)
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) SELECT $1,'page-user-'||n,'WORKER' FROM generate_series(1,137)n`, org)
	exec(`INSERT INTO training_courses(organization_id,code,title,status) SELECT $1,'course-'||n,'Curso repetido','PUBLISHED' FROM generate_series(1,137)n`, org)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) SELECT $1,'worker',md5('location-'||n)::uuid FROM generate_series(1,137)n`, org)
	exec(`INSERT INTO incident_catalog(id,organization_id,code,name,area,normative_criterion,risk_level) VALUES(md5('catalog')::uuid,$1,'prueba','Incidencia','Cocina','Norma','LOW')`, org)
	exec(`INSERT INTO incidents(id,organization_id,location_id,catalog_id,reported_by,client_operation_id,client_created_at) VALUES(md5('incident')::uuid,$1,md5('location-137')::uuid,md5('catalog')::uuid,'worker',md5('operation')::uuid,NOW())`, org)
	seen := map[string]bool{}
	token := ""
	pages := 0
	for {
		p, e := service.OrganizationsPage(ctx, "root", pagination.Request{Size: 11, Token: token, Query: "Mismo"})
		if e != nil {
			t.Fatal(e)
		}
		pages++
		if len(p.Items) > 11 {
			t.Fatal("unbounded page")
		}
		for _, o := range p.Items {
			if seen[o.ID] {
				t.Fatal("duplicate tie")
			}
			seen[o.ID] = true
		}
		token = p.NextToken
		if token == "" {
			break
		}
		if pages > 20 {
			t.Fatal("endless pages")
		}
	}
	if len(seen) != 137 {
		t.Fatal("lost organizations", len(seen))
	}
	first, e := service.OrganizationsPage(ctx, "root", pagination.Request{Size: 3, Query: "Mismo"})
	if e != nil {
		t.Fatal(e)
	}
	// Borrar la clave de anclaje no impide continuar: el cursor contiene la tupla completa.
	exec(`DELETE FROM organizations WHERE id=$1`, first.Items[2].ID)
	second, e := service.OrganizationsPage(ctx, "root", pagination.Request{Size: 3, Query: "Mismo", Token: first.NextToken})
	if e != nil || len(second.Items) != 3 {
		t.Fatal("deleted anchor broke continuation", e)
	}
	for _, resource := range []string{"locations", "members", "courses"} {
		ids := map[string]bool{}
		next := ""
		for {
			var got []string
			switch resource {
			case "locations":
				p, e := service.LocationsPage(ctx, "admin", org, pagination.Request{Size: 19, Token: next, Query: "Sede"})
				if e != nil {
					t.Fatal(e)
				}
				next = p.NextToken
				for _, v := range p.Items {
					got = append(got, v.ID)
				}
			case "members":
				p, e := service.MembersPage(ctx, "admin", org, pagination.Request{Size: 19, Token: next, Query: "Mismo"})
				if e != nil {
					t.Fatal(e)
				}
				next = p.NextToken
				for _, v := range p.Items {
					got = append(got, v.ID)
				}
			default:
				p, e := service.EntriesPage(ctx, "admin", org, resource, pagination.Request{Size: 19, Token: next, Query: "Curso repetido"})
				if e != nil {
					t.Fatal(e)
				}
				next = p.NextToken
				for _, v := range p.Items {
					got = append(got, v.ID)
				}
			}
			for _, id := range got {
				if ids[id] {
					t.Fatal("duplicate", resource)
				}
				ids[id] = true
			}
			if next == "" {
				break
			}
			if len(ids) > 200 {
				t.Fatal("endless", resource)
			}
		}
		if len(ids) != 137 {
			t.Fatal("missing records", resource, len(ids))
		}
	}
	w, e := service.WorkspacePage(ctx, "admin", org, 7)
	if e != nil || len(w.Locations) != 7 || w.NextTokens["locations"] == "" || w.Totals["locations"] != 138 || w.Totals["courses"] != 138 {
		t.Fatal("incorrect initial page or totals", e, w.Totals)
	}
	router := chi.NewRouter()
	organizationhttp.New(service, slog.New(slog.NewTextHandler(io.Discard, nil))).Register(router, middleware.Authenticate(tenantTestVerifier{}))
	for _, resource := range []string{"locations", "members", "courses", "checklist-summaries"} {
		request := httptest.NewRequest("GET", "/organizations/"+org+"/"+resource+"?pageSize=3", nil)
		request.Header.Set("Authorization", "Bearer admin")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		var envelope struct {
			Data       []json.RawMessage
			Pagination struct{ NextPageToken string }
		}
		if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &envelope) != nil || envelope.Data == nil || len(envelope.Data) > 3 {
			t.Fatal("HTTP page contract", resource, recorder.Code, recorder.Body.String())
		}
		if resource != "incidents" && resource != "checklist-summaries" && envelope.Pagination.NextPageToken == "" {
			t.Fatal("HTTP lost cursor", resource)
		}
	}

	for _, actor := range []string{"admin", "worker"} {
		p, e := service.EntriesPage(ctx, actor, org, "incidents", pagination.Request{})
		if e != nil || len(p.Items) != 1 {
			t.Fatal("incident hidden after first 100 sites", actor, e)
		}
	}
	// Las incidencias mantienen fecha descendente y desempate por ID, incluso con fechas iguales.
	exec(`INSERT INTO incidents(id,organization_id,location_id,catalog_id,reported_by,client_operation_id,client_created_at,created_at)
 SELECT md5('incident-more-'||n)::uuid,$1,md5('location-'||n)::uuid,md5('catalog')::uuid,'admin',md5('incident-operation-'||n)::uuid,NOW(),'2026-01-01T00:00:00Z'::timestamptz+(n%4)*interval '1 second' FROM generate_series(1,137)n`, org)
	incidentIDs := map[string]bool{}
	nextIncident := ""
	lastTime := ""
	lastID := ""
	for {
		p, e := service.EntriesPage(ctx, "admin", org, "incidents", pagination.Request{Size: 13, Token: nextIncident})
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range p.Items {
			if incidentIDs[v.ID] || (lastTime != "" && (v.CursorKey > lastTime || v.CursorKey == lastTime && v.ID <= lastID)) {
				t.Fatal("incident order or duplicate", v.ID)
			}
			incidentIDs[v.ID] = true
			lastTime = v.CursorKey
			lastID = v.ID
		}
		nextIncident = p.NextToken
		if nextIncident == "" {
			break
		}
		if len(incidentIDs) > 150 {
			t.Fatal("endless incidents")
		}
	}
	if len(incidentIDs) != 138 {
		t.Fatal("lost incidents", len(incidentIDs))
	}
	workerIncidents, e := service.EntriesPage(ctx, "worker", org, "incidents", pagination.Request{})
	if e != nil || len(workerIncidents.Items) != 1 {
		t.Fatal("worker saw another reporter", e)
	}

	if _, e = service.MembersPage(ctx, "worker", org, pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("worker read team", e)
	}
	if _, e = service.LocationsPage(ctx, "admin", other, pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("foreign tenant", e)
	}
	lp, e := service.LocationsPage(ctx, "worker", org, pagination.Request{Size: 2})
	if e != nil || lp.NextToken == "" {
		t.Fatal(e)
	}
	if _, e = service.LocationsPage(ctx, "worker", other, pagination.Request{Size: 2, Token: lp.NextToken}); !errors.Is(e, pagination.ErrInvalid) {
		t.Fatal("foreign cursor accepted", e)
	}
	exec(`UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='worker'`, org)
	if _, e = service.LocationsPage(ctx, "worker", org, pagination.Request{Size: 2, Token: lp.NextToken}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("cursor bypassed revoked membership", e)
	}
	// Los comodines de SQL se buscan como caracteres literales.
	p, e := service.OrganizationsPage(ctx, "root", pagination.Request{Query: "%_"})
	if e != nil || len(p.Items) != 0 {
		t.Fatal("wildcard widened scope", e)
	}
	t.Log(fmt.Sprintf("paginación comprobada: %d organizaciones y 137 sedes, personas y cursos", len(seen)))
}
