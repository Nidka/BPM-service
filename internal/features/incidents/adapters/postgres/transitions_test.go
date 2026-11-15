package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/features/incidents/application"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
	"testing"
	"time"
)

func execSQL(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, e := pool.Exec(ctx, query, args...); e != nil {
		t.Fatal(e)
	}
}
func setupIncident(t *testing.T) (context.Context, *pgxpool.Pool, *application.Service, domain.Report) {
	t.Helper()
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool))
	c := domain.Catalog{ID: catalogID, Code: "TEST", Name: "Superficie", Area: "Cocina", NormativeCriterion: "Criterio", RiskLevel: "LOW"}
	if _, e := s.CreateCatalog(ctx, "admin", org, "catalog-key", c); e != nil {
		t.Fatal(e)
	}
	r := domain.Report{ID: uid(4, 1), LocationID: site, CatalogID: catalogID, Description: "Reparar superficie", ClientOperationID: uid(5, 1), ClientCreatedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
	if _, e := s.Report(ctx, "worker", org, "report-key", r); e != nil {
		t.Fatal(e)
	}
	return ctx, pool, s, r
}
func TestSelfVerificationReturnAndStaleConcurrentCommands(t *testing.T) {
	ctx, pool, s, r := setupIncident(t)
	act := func(user, key string, c domain.Command) domain.Incident {
		t.Helper()
		v, e := s.Act(ctx, user, org, r.ID, key, c)
		if e != nil {
			t.Fatal(key, e)
		}
		return v
	}
	assign := domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "chef", Description: "Reparar", DueAt: time.Now().Add(time.Hour)}
	act("admin", "assign-key", assign)
	act("chef", "start-key", domain.Command{OperationID: uid(6, 2), RowVersion: 2, ActionType: "START"})
	resolve := domain.Command{OperationID: uid(6, 3), RowVersion: 3, ActionType: "RESOLVE", Note: "Reparación completa"}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := resolve
			c.OperationID = uid(6, 3+i)
			_, e := s.Act(ctx, "chef", org, r.ID, fmt.Sprintf("resolve-key-%d", i), c)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal(success, conflicts)
	}
	for _, command := range []string{"VERIFY", "RETURN"} {
		if _, e := s.Act(ctx, "chef", org, r.ID, "self-"+command, domain.Command{OperationID: uid(6, 7), RowVersion: 4, ActionType: command, Note: "Conforme"}); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("autovalidación", command, e)
		}
	}
	returned := act("admin", "return-key", domain.Command{OperationID: uid(6, 8), RowVersion: 4, ActionType: "RETURN", Note: "Falta un borde"})
	if returned.Status != "IN_PROGRESS" || returned.Actions[0].ResolvedAt != nil || returned.RowVersion != 5 {
		t.Fatal(returned)
	}
	act("chef", "resolve-again", domain.Command{OperationID: uid(6, 9), RowVersion: 5, ActionType: "RESOLVE", Note: "Borde corregido"})
	closed := act("admin", "verify-key", domain.Command{OperationID: uid(6, 10), RowVersion: 6, ActionType: "VERIFY", Note: "Verificado"})
	if closed.ClosedAt == nil || closed.Actions[0].VerifiedBy == nil || *closed.Actions[0].VerifiedBy != "admin" {
		t.Fatal(closed)
	}
	history, e := s.History(ctx, "chef", org, r.ID, pagination.Request{})
	if e != nil || len(history.Items) != 7 {
		t.Fatal(history, e)
	}
	foundReturn := false
	for _, event := range history.Items {
		if event.Note == "Falta un borde" && event.FromStatus == "RESOLVED" && event.ToStatus == "IN_PROGRESS" {
			foundReturn = true
		}
	}
	if !foundReturn {
		t.Fatal("perdió evento de devolución")
	}
	execSQL(t, ctx, pool, `DELETE FROM membership_locations WHERE organization_id=$1 AND user_id='chef'`, org)
	if _, e = s.Act(ctx, "chef", org, r.ID, "start-key", domain.Command{OperationID: uid(6, 2), RowVersion: 2, ActionType: "START"}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("replay con sede revocada", e)
	}
}
func TestCatalogAndAssigneePagesUseCurrentContext(t *testing.T) {
	ctx, pool, s, r := setupIncident(t)
	c := domain.Catalog{ID: uid(3, 2), Code: "SECOND", Name: "Ventilación", Area: "Cocina", NormativeCriterion: "Criterio", RiskLevel: "LOW"}
	if _, e := s.CreateCatalog(ctx, "admin", org, "catalog-two", c); e != nil {
		t.Fatal(e)
	}
	c.ID = uid(3, 3)
	c.Name = "Ventilación actual"
	if _, e := s.VersionCatalog(ctx, "admin", org, uid(3, 2), "catalog-version", domain.CatalogVersion{Catalog: c, ExpectedVersion: 1}); e != nil {
		t.Fatal(e)
	}
	p, e := s.Catalogs(ctx, "worker", org, pagination.Request{Size: 1})
	if e != nil || len(p.Items) != 1 || p.NextToken == "" {
		t.Fatal(p, e)
	}
	p, e = s.Catalogs(ctx, "worker", org, pagination.Request{Token: p.NextToken, Size: 1})
	if e != nil || len(p.Items) != 1 || p.Items[0].ID != uid(3, 3) || p.Items[0].VersionNumber != 2 || p.NextToken != "" {
		t.Fatal(p, e)
	}
	p, e = s.Catalogs(ctx, "worker", org, pagination.Request{Query: "SECOND"})
	if e != nil || len(p.Items) != 1 || p.Items[0].VersionNumber != 2 {
		t.Fatal(p, e)
	}
	if _, e = s.VersionCatalog(ctx, "admin", org, uid(3, 2), "stale-version", domain.CatalogVersion{Catalog: c, ExpectedVersion: 1}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("versionó versión antigua", e)
	}
	users, e := s.Assignees(ctx, "chef", org, r.ID, pagination.Request{Size: 1})
	if e != nil || len(users.Items) != 1 || users.NextToken == "" {
		t.Fatal(users, e)
	}
	filtered, e := s.Assignees(ctx, "chef", org, r.ID, pagination.Request{Query: "worker2@example.invalid"})
	if e != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != "worker2" {
		t.Fatal(filtered, e)
	}
	if _, e = s.Assignees(ctx, "worker", org, r.ID, pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("worker listó personas", e)
	}
	execSQL(t, ctx, pool, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='worker2'`, org)
	filtered, e = s.Assignees(ctx, "chef", org, r.ID, pagination.Request{Query: "worker2@example.invalid"})
	if e != nil || len(filtered.Items) != 0 {
		t.Fatal(filtered, e)
	}
	execSQL(t, ctx, pool, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='chef'`, org)
	if _, e = s.Assignees(ctx, "chef", org, r.ID, pagination.Request{Token: users.NextToken}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("cursor omitió membresía", e)
	}
}
func TestVerifySerializesPhotoMutationOnIncident(t *testing.T) {
	ctx, pool, s, r := setupIncident(t)
	if _, e := s.Act(ctx, "chef", org, r.ID, "assign-key", domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "worker", Description: "Reparar", DueAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Act(ctx, "worker", org, r.ID, "start-key", domain.Command{OperationID: uid(6, 2), RowVersion: 2, ActionType: "START"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Act(ctx, "worker", org, r.ID, "resolve-key", domain.Command{OperationID: uid(6, 3), RowVersion: 3, ActionType: "RESOLVE", Note: "Reparación completa"}); e != nil {
		t.Fatal(e)
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SELECT id FROM incidents WHERE organization_id=$1 AND id=$2 FOR UPDATE`, org, r.ID); e != nil {
		t.Fatal(e)
	}
	blocked, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, e = s.Act(blocked, "chef", org, r.ID, "verify-key", domain.Command{OperationID: uid(6, 4), RowVersion: 4, ActionType: "VERIFY", Note: "Conforme"}); e == nil {
		t.Fatal("verify no respetó bloqueo de media")
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := s.Detail(ctx, "chef", org, r.ID)
	if e != nil || v.Status != "RESOLVED" || v.RowVersion != 4 {
		t.Fatal(v, e)
	}
}
func TestIncidentPagingUsesDescendingDateAscendingIDAndContext(t *testing.T) {
	ctx, pool, s, r := setupIncident(t)
	for i := 2; i <= 3; i++ {
		v := r
		v.ID = uid(4, i)
		v.ClientOperationID = uid(5, i)
		if _, e := s.Report(ctx, "worker", org, fmt.Sprintf("report-page-%d", i), v); e != nil {
			t.Fatal(e)
		}
	}
	execSQL(t, ctx, pool, `UPDATE incidents SET created_at='2026-01-01T00:00:00Z' WHERE organization_id=$1`, org)
	p, e := s.Incidents(ctx, "chef", org, site, "OPEN", pagination.Request{Size: 1, Query: "superficie"})
	if e != nil || len(p.Items) != 1 || p.Items[0].ID != uid(4, 1) || p.NextToken == "" {
		t.Fatal("desempate", p, e)
	}
	next, e := s.Incidents(ctx, "chef", org, site, "OPEN", pagination.Request{Size: 1, Query: "superficie", Token: p.NextToken})
	if e != nil || len(next.Items) != 1 || next.Items[0].ID != uid(4, 2) {
		t.Fatal(next, e)
	}
	for _, change := range []struct{ site, status, query string }{{otherSite, "OPEN", "superficie"}, {site, "CLOSED", "superficie"}, {site, "OPEN", "otra"}} {
		if _, e = s.Incidents(ctx, "chef", org, change.site, change.status, pagination.Request{Query: change.query, Token: p.NextToken}); !errors.Is(e, pagination.ErrInvalid) {
			t.Fatal("cursor cruzó filtros", change, e)
		}
	}
	execSQL(t, ctx, pool, `UPDATE locations SET status='INACTIVE' WHERE id=$1`, site)
	if _, e = s.Incidents(ctx, "chef", org, site, "OPEN", pagination.Request{Query: "superficie", Token: p.NextToken}); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("cursor omitió sede inactiva", e)
	}
}
func TestOperationReplayCanonicalizesUUIDPath(t *testing.T) {
	ctx, _, s, r := setupIncident(t)
	r.ID = "a0000004-0000-0000-0000-000000000002"
	r.ClientOperationID = uid(5, 2)
	if _, e := s.Report(ctx, "worker", org, "report-letter-key", r); e != nil {
		t.Fatal(e)
	}
	c := domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "worker", Description: "Reparar", DueAt: time.Now().Add(time.Hour)}
	if _, e := s.Act(ctx, "chef", org, r.ID, "assign-letter-key", c); e != nil {
		t.Fatal(e)
	}
	v, e := s.Act(ctx, "chef", org, "A0000004-0000-0000-0000-000000000002", "replay-letter-key", c)
	if e != nil || v.Status != "ASSIGNED" || v.RowVersion != 2 {
		t.Fatal("UUID equivalente cambió hash", v, e)
	}
}

// El ADMIN opera en cualquier sede sin membresía de sede, es asignable y nunca
// valida su propia corrección; otro ADMIN (o el mismo en otra acción) sí valida.
func TestAdminOperatesInAnySiteAndNeverVerifiesOwnCorrection(t *testing.T) {
	ctx, pool, s, r := setupIncident(t)
	execSQL(t, ctx, pool, `INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin2','admin2@example.invalid','Admin 2','USER'),('admin-foreign','af@example.invalid','Admin ajeno','USER')`)
	execSQL(t, ctx, pool, `INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin2','ADMIN'),($2,'admin-foreign','ADMIN')`, org, foreign)
	list, e := s.Assignees(ctx, "admin", org, r.ID, pagination.Request{Query: "admin2@example.invalid"})
	if e != nil || len(list.Items) != 1 || list.Items[0].ID != "admin2" {
		t.Fatal("el ADMIN debe ser asignable", list, e)
	}
	if foreignList, e := s.Assignees(ctx, "admin", org, r.ID, pagination.Request{Query: "af@example.invalid"}); e != nil || len(foreignList.Items) != 0 {
		t.Fatal("ADMIN de otra empresa asignable", foreignList, e)
	}
	// Reporta en una sede sin membresía de sede.
	rep := domain.Report{ID: uid(4, 9), LocationID: otherSite, CatalogID: catalogID, Description: "Fuga en cocina", ClientOperationID: uid(5, 9), ClientCreatedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}
	if _, e = s.Report(ctx, "admin", org, "admin-report-key", rep); e != nil {
		t.Fatal("ADMIN no pudo reportar", e)
	}
	if _, e = s.Report(ctx, "admin-foreign", org, "foreign-report-key", domain.Report{ID: uid(4, 10), LocationID: site, CatalogID: catalogID, Description: "Intruso", ClientOperationID: uid(5, 10), ClientCreatedAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}); e == nil {
		t.Fatal("ADMIN de otra empresa reportó")
	}
	act := func(user, key string, c domain.Command) error {
		_, e := s.Act(ctx, user, org, r.ID, key, c)
		return e
	}
	if e = act("admin", "assign-admin2-key", domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "admin2", Description: "Reparar", DueAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if e = act("admin2", "start-admin2-key", domain.Command{OperationID: uid(6, 2), RowVersion: 2, ActionType: "START"}); e != nil {
		t.Fatal(e)
	}
	if e = act("admin2", "resolve-admin2-key", domain.Command{OperationID: uid(6, 3), RowVersion: 3, ActionType: "RESOLVE", Note: "Reparación completa"}); e != nil {
		t.Fatal(e)
	}
	for _, c := range []string{"VERIFY", "RETURN"} {
		if e = act("admin2", "self-"+c, domain.Command{OperationID: uid(6, 4), RowVersion: 4, ActionType: c, Note: "Conforme"}); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("el ADMIN responsable validó su corrección", c, e)
		}
	}
	if e = act("admin", "return-admin-key", domain.Command{OperationID: uid(6, 5), RowVersion: 4, ActionType: "RETURN", Note: "Falta un borde"}); e != nil {
		t.Fatal("otro ADMIN debe poder devolver", e)
	}
}

// Sin otra persona elegible, el dueño (ADMIN) puede validar su propia corrección con nota;
// si existe otro ADMIN o un CHEF de la sede, sigue prohibido.
func TestSoleAdminSelfValidatesWithNoteAndIsAudited(t *testing.T) {
	ctx, pool, s, r := setupIncident(t)
	act := func(user, key string, c domain.Command) (domain.Incident, error) {
		return s.Act(ctx, user, org, r.ID, key, c)
	}
	if _, e := act("admin", "assign-self-key", domain.Command{OperationID: uid(6, 1), RowVersion: 1, ActionType: "ASSIGN", ActionID: uid(7, 1), ResponsibleUserID: "admin", Description: "Reparar", DueAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if _, e := act("admin", "start-self-key", domain.Command{OperationID: uid(6, 2), RowVersion: 2, ActionType: "START"}); e != nil {
		t.Fatal(e)
	}
	if _, e := act("admin", "resolve-self-key", domain.Command{OperationID: uid(6, 3), RowVersion: 3, ActionType: "RESOLVE", Note: "Reparación completa"}); e != nil {
		t.Fatal(e)
	}
	verify := domain.Command{OperationID: uid(6, 4), RowVersion: 4, ActionType: "VERIFY", Note: "Verificado por el dueño"}
	// El CHEF de la sede está activo: sigue prohibido y no se ofrece la opción.
	if _, e := act("admin", "self-verify-chef", verify); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("autovalidó con un CHEF elegible", e)
	}
	if d, e := s.Detail(ctx, "admin", org, r.ID); e != nil || len(d.AllowedActions) != 0 {
		t.Fatal(d.AllowedActions, e)
	}
	// Otro ADMIN activo (CHEF desactivado): sigue prohibido.
	execSQL(t, ctx, pool, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='chef'`, org)
	execSQL(t, ctx, pool, `INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin2','a2@example.invalid','Admin 2','USER')`)
	execSQL(t, ctx, pool, `INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin2','ADMIN')`, org)
	if _, e := act("admin", "self-verify-admin2", verify); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("autovalidó con otro ADMIN elegible", e)
	}
	// Un CHEF de otra sede no cuenta como validador de esta sede.
	execSQL(t, ctx, pool, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='admin2'`, org)
	d, e := s.Detail(ctx, "admin", org, r.ID)
	if e != nil || len(d.AllowedActions) != 1 || d.AllowedActions[0] != "VERIFY" {
		t.Fatal("debe ofrecerse VERIFY", d.AllowedActions, e)
	}
	noNote := verify
	noNote.Note = ""
	var invalid *domain.ValidationError
	if _, e = act("admin", "self-verify-nonote", noNote); !errors.As(e, &invalid) {
		t.Fatal("sin nota debe ser inválido", e)
	}
	if _, e = act("admin", "self-return-key", domain.Command{OperationID: uid(6, 5), RowVersion: 4, ActionType: "RETURN", Note: "Falta"}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("RETURN propio permitido", e)
	}
	closed, e := act("admin", "self-verify-ok", verify)
	if e != nil || closed.Status != "CLOSED" || len(closed.Actions) != 1 || !closed.Actions[0].SelfValidated() {
		t.Fatal(closed, e)
	}
	var n int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action='incident.SELF_VERIFY' AND entity_id=$2`, org, r.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal("auditoría de autovalidación", n, e)
	}
}
