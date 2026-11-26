package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// accountsPool crea un esquema aislado con la cadena de migraciones real hasta V12.
func accountsPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires database bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("cannot connect to test database")
	}
	schema := pgx.Identifier{fmt.Sprintf("accounts_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.MaxConns = 8
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, n := range []string{"V1__baseline.sql", "V2__authentication_sessions.sql", "V4__checklist_evidence_items.sql", "V5__daily_operations.sql", "V6__incidents.sql", "V7__alerts_notifications.sql", "V8__training_academy.sql", "V12__accounts_management.sql"} {
		b, e := os.ReadFile("../../../../../migraciones/" + n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(b)); e != nil {
			t.Fatal(n, e)
		}
	}
	return pool, ctx
}

func TestAccountsManagement(t *testing.T) {
	pool, ctx := accountsPool(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	const (
		org1, org2 = "org-one", "org-two"
		l1, l2     = "20000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002"
		lo         = "20000000-0000-0000-0000-000000000009"
	)
	exec(`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Uno','RESTAURANT'),($2,'Dos','RESTAURANT')`, org1, org2)
	exec(`INSERT INTO locations(id,organization_id,name,address) VALUES($1,$3,'Centro','Av 1'),($2,$3,'Norte','Av 2'),($4,$5,'Otra','Av 9')`, l1, l2, org1, lo, org2)
	for _, u := range []string{"root", "a1", "a2", "chef", "worker", "outsider", "ob", "invitee"} {
		role := "USER"
		if u == "root" {
			role = "SUPER_ADMIN"
		}
		exec(`INSERT INTO application_users(id,email,display_name,platform_role) VALUES($1,$2,$1,$3)`, u, u+"@example.invalid", role)
	}
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'a1','ADMIN'),($1,'a2','ADMIN'),($1,'chef','CHEF'),($1,'worker','WORKER'),($2,'ob','ADMIN')`, org1, org2)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2),($1,'worker',$2)`, org1, l1)
	svc := application.New(New(pool))
	is := func(err, want error, what string) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("%s: got %v want %v", what, err, want)
		}
	}
	member := func(uid string) domain.Member {
		t.Helper()
		var m domain.Member
		if e := pool.QueryRow(ctx, memberSelect, org1, uid).Scan(&m.ID, &m.Name, &m.Email, &m.Role, &m.LocationIDs, &m.Status, &m.RowVersion, &m.AccountActive); e != nil {
			t.Fatal(e)
		}
		return m
	}

	// Rol insuficiente y tenant ajeno.
	w := member("worker")
	_, err := svc.UpdateMember(ctx, "chef", org1, "worker", application.MemberUpdate{Role: "CHEF", LocationIDs: []string{l1}, RowVersion: w.RowVersion})
	is(err, domain.ErrDenied, "chef edits member")
	_, err = svc.DeactivateMember(ctx, "worker", org1, "chef", 1)
	is(err, domain.ErrDenied, "worker deactivates")
	_, err = svc.UpdateMember(ctx, "ob", org1, "worker", application.MemberUpdate{Role: "CHEF", LocationIDs: []string{l1}, RowVersion: w.RowVersion})
	is(err, domain.ErrDenied, "foreign admin edits member")
	_, err = svc.UpdateMember(ctx, "outsider", org1, "worker", application.MemberUpdate{Role: "CHEF", LocationIDs: []string{l1}, RowVersion: w.RowVersion})
	is(err, domain.ErrDenied, "outsider edits member")
	_, err = svc.UpdateMember(ctx, "a1", org1, "ob", application.MemberUpdate{Role: "ADMIN", RowVersion: 1})
	is(err, domain.ErrNotFound, "member of another tenant is 404")
	_, _, err = svc.Invite(ctx, "chef", org1, application.InvitationInput{ID: "30000000-0000-0000-0000-000000000001", Email: "x@example.invalid", Role: "ADMIN"})
	is(err, domain.ErrDenied, "chef invites")
	_, err = svc.UpdateOrganization(ctx, "chef", org1, domain.Organization{LegalName: "Nuevo", BusinessType: "CATERING"}, 1)
	is(err, domain.ErrDenied, "chef edits organization")
	_, err = svc.UpdateLocation(ctx, "a1", org1, lo, domain.Location{Name: "x1", Address: "abc", Timezone: "America/Lima", Status: "ACTIVE"}, 1)
	is(err, domain.ErrNotFound, "foreign location is 404")

	// Edición de rol/sedes con versión.
	upd, err := svc.UpdateMember(ctx, "a1", org1, "worker", application.MemberUpdate{Role: "CHEF", LocationIDs: []string{l1, l2}, RowVersion: w.RowVersion})
	if err != nil || upd.Role != "CHEF" || len(upd.LocationIDs) != 2 || upd.RowVersion != w.RowVersion+1 {
		t.Fatal("update member", upd, err)
	}
	_, err = svc.UpdateMember(ctx, "a1", org1, "worker", application.MemberUpdate{Role: "WORKER", LocationIDs: []string{l1}, RowVersion: w.RowVersion})
	is(err, domain.ErrVersionConflict, "stale version")
	_, err = svc.UpdateMember(ctx, "a1", org1, "worker", application.MemberUpdate{Role: "WORKER", LocationIDs: []string{lo}, RowVersion: upd.RowVersion})
	var validation *domain.ValidationError
	if !errors.As(err, &validation) {
		t.Fatal("foreign location accepted", err)
	}

	// Concurrencia: dos ediciones con la misma versión, solo una gana.
	cur := member("worker")
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, role := range []string{"WORKER", "CHEF"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = svc.UpdateMember(ctx, "a1", org1, "worker", application.MemberUpdate{Role: role, LocationIDs: []string{l1}, RowVersion: cur.RowVersion})
		}()
	}
	wg.Wait()
	if (results[0] == nil) == (results[1] == nil) || (results[0] != nil && !errors.Is(results[0], domain.ErrVersionConflict)) && (results[1] != nil && !errors.Is(results[1], domain.ErrVersionConflict)) {
		t.Fatal("concurrent edits", results)
	}

	// Último ADMIN: concurrentemente dos ADMIN se degradan; al menos uno debe seguir activo.
	a1, a2 := member("a1"), member("a2")
	errs := make([]error, 2)
	for i, c := range []struct {
		actor, target string
		v             int
	}{{"a1", "a2", a2.RowVersion}, {"a2", "a1", a1.RowVersion}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = svc.UpdateMember(ctx, c.actor, org1, c.target, application.MemberUpdate{Role: "CHEF", LocationIDs: []string{l1}, RowVersion: c.v})
		}()
	}
	wg.Wait()
	var admins int
	pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE organization_id=$1 AND role='ADMIN' AND status='ACTIVE'`, org1).Scan(&admins)
	if admins != 1 {
		t.Fatal("concurrent demotion left admins:", admins, errs)
	}
	var last string
	pool.QueryRow(ctx, `SELECT user_id FROM organization_memberships WHERE organization_id=$1 AND role='ADMIN' AND status='ACTIVE'`, org1).Scan(&last)
	lm := member(last)
	_, err = svc.UpdateMember(ctx, last, org1, last, application.MemberUpdate{Role: "WORKER", LocationIDs: []string{l1}, RowVersion: lm.RowVersion})
	is(err, domain.ErrLastAdmin, "last admin demotion")
	_, err = svc.DeactivateMember(ctx, last, org1, last, lm.RowVersion)
	is(err, domain.ErrLastAdmin, "last admin deactivation")
	_, err = svc.DeactivateMember(ctx, "root", org1, last, lm.RowVersion)
	is(err, domain.ErrLastAdmin, "super admin cannot orphan the org either")

	// Transferencia de administración.
	other := "a1"
	if last == "a1" {
		other = "a2"
	}
	om := member(other)
	_, _, err = svc.TransferAdmin(ctx, last, org1, other, application.TransferInput{RowVersion: om.RowVersion, ActorRole: "WORKER"})
	if !errors.As(err, &validation) {
		t.Fatal("transfer without locations", err)
	}
	na, fo, err := svc.TransferAdmin(ctx, last, org1, other, application.TransferInput{RowVersion: om.RowVersion, ActorRole: "WORKER", ActorLocationIDs: []string{l1}})
	if err != nil || na.Role != "ADMIN" || fo.Role != "WORKER" || na.ID != other {
		t.Fatal("transfer", na, fo, err)
	}
	// El nuevo ADMIN ya no puede quedar solo y desactivar al anterior inexistente: sigue habiendo uno.
	_, err = svc.DeactivateMember(ctx, other, org1, other, na.RowVersion)
	is(err, domain.ErrLastAdmin, "new sole admin")

	// Baja lógica: sesiones revocadas, dispositivos retirados, no opera, reactivación.
	exec(`INSERT INTO authentication_sessions(user_id,authenticated_at,last_activity_at) VALUES('chef',NOW(),NOW())`)
	exec(`INSERT INTO mobile_devices(id,organization_id,user_id,installation_id,platform,push_token,token_hash) VALUES('40000000-0000-0000-0000-000000000001',$1,'chef','inst-1','ANDROID','tok','h1'),('40000000-0000-0000-0000-000000000002',$1,'worker','inst-2','IOS','tok2','h2')`, org1)
	ch := member("chef")
	if _, err = svc.Workspace(ctx, "chef", org1); err != nil {
		t.Fatal("active chef cannot read workspace", err)
	}
	dm, err := svc.DeactivateMember(ctx, other, org1, "chef", ch.RowVersion)
	if err != nil || dm.Status != "INACTIVE" {
		t.Fatal("deactivate", dm, err)
	}
	var revoked bool
	var devStatus string
	pool.QueryRow(ctx, `SELECT revoked FROM authentication_sessions WHERE user_id='chef'`).Scan(&revoked)
	pool.QueryRow(ctx, `SELECT status FROM mobile_devices WHERE user_id='chef'`).Scan(&devStatus)
	if !revoked || devStatus != "INACTIVE" {
		t.Fatal("sessions/devices not cut", revoked, devStatus)
	}
	if _, err = svc.Workspace(ctx, "chef", org1); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("inactive member operates", err)
	}
	_, err = svc.InvitationsPage(ctx, "chef", org1, "", pagination.Request{Size: 10})
	is(err, domain.ErrDenied, "inactive member lists invitations")
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE organization_id=$1 AND user_id='chef'`, org1).Scan(&n)
	if n != 1 {
		t.Fatal("physical delete")
	}
	inactive, err := svc.MembersByStatus(ctx, other, org1, "INACTIVE", pagination.Request{Size: 10})
	if err != nil || len(inactive.Items) != 1 || inactive.Items[0].ID != "chef" {
		t.Fatal("inactive list", inactive, err)
	}
	_, err = svc.MembersByStatus(ctx, "worker", org1, "INACTIVE", pagination.Request{Size: 10})
	is(err, domain.ErrDenied, "worker lists inactive members")
	re, err := svc.ReactivateMember(ctx, other, org1, "chef", dm.RowVersion)
	if err != nil || re.Status != "ACTIVE" {
		t.Fatal("reactivate", re, err)
	}
	if _, err = svc.Workspace(ctx, "chef", org1); err != nil {
		t.Fatal("reactivated member denied", err)
	}

	// Organización y sede: edición, tipo de establecimiento, auditoría.
	var rv int
	pool.QueryRow(ctx, `SELECT row_version FROM organizations WHERE id=$1`, org1).Scan(&rv)
	_, err = svc.UpdateOrganization(ctx, other, org1, domain.Organization{LegalName: "Uno SAC", BusinessType: "BAR"}, rv)
	if !errors.As(err, &validation) || validation.Fields["businessType"] == "" {
		t.Fatal("invalid business type", err)
	}
	o, err := svc.UpdateOrganization(ctx, other, org1, domain.Organization{LegalName: "Uno SAC", BusinessType: "CAFETERIA"}, rv)
	if err != nil || o.BusinessType != "CAFETERIA" || o.RowVersion != rv+1 {
		t.Fatal("update org", o, err)
	}
	_, err = svc.UpdateOrganization(ctx, other, org1, domain.Organization{LegalName: "Uno SAC", BusinessType: "CATERING"}, rv)
	is(err, domain.ErrVersionConflict, "org stale version")
	o, err = svc.UpdateOrganization(ctx, "root", org1, domain.Organization{LegalName: "Uno SAC", BusinessType: "RELATED_SERVICE"}, rv+1)
	if err != nil || o.BusinessType != "OTHER_FOOD_SERVICE" {
		t.Fatal("legacy alias", o, err)
	}
	ws, err := svc.Workspace(ctx, "worker", org1)
	if err != nil || ws.Organization.BusinessType != "OTHER_FOOD_SERVICE" {
		t.Fatal("workspace business type", ws.Organization, err)
	}
	var lv int
	pool.QueryRow(ctx, `SELECT row_version FROM locations WHERE id=$1`, l2).Scan(&lv)
	_, err = svc.UpdateLocation(ctx, other, org1, l2, domain.Location{Name: "Norte", Address: "Av 2", Timezone: "Mars/Base", Status: "ACTIVE"}, lv)
	if !errors.As(err, &validation) || validation.Fields["timezone"] == "" {
		t.Fatal("invalid timezone", err)
	}
	loc, err := svc.UpdateLocation(ctx, other, org1, l2, domain.Location{Name: "Norte 2", Address: "Av 2", Timezone: "America/Bogota", Status: "INACTIVE"}, lv)
	if err != nil || loc.Status != "INACTIVE" || loc.Timezone != "America/Bogota" {
		t.Fatal("update location", loc, err)
	}
	_, err = svc.UpdateLocation(ctx, other, org1, l2, domain.Location{Name: "Norte 3", Address: "Av 2", Timezone: "America/Lima", Status: "ACTIVE"}, lv)
	is(err, domain.ErrVersionConflict, "location stale version")
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND action IN ('organization.updated','location.updated','membership.deactivated','membership.reactivated','membership.updated')`, org1).Scan(&n)
	if n < 6 {
		t.Fatal("missing audit events", n)
	}
	inactiveLocs, err := svc.LocationsByStatus(ctx, other, org1, "INACTIVE", pagination.Request{Size: 10})
	if err != nil || len(inactiveLocs.Items) != 1 {
		t.Fatal("inactive locations", inactiveLocs, err)
	}
	_, err = svc.LocationsByStatus(ctx, "worker", org1, "INACTIVE", pagination.Request{Size: 10})
	is(err, domain.ErrDenied, "worker lists inactive locations")

	// Invitaciones.
	const inv1 = "30000000-0000-0000-0000-000000000001"
	tokenOf := func(id string) string {
		t.Helper()
		var raw []byte
		if e := pool.QueryRow(ctx, `SELECT payload FROM outbox_events WHERE aggregate_id=$1 AND status='PENDING' ORDER BY created_at DESC LIMIT 1`, id).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		var p map[string]string
		json.Unmarshal(raw, &p)
		return p["token"]
	}
	in := application.InvitationInput{ID: inv1, Email: "Invitee@Example.invalid", Role: "WORKER", LocationIDs: []string{l1}}
	iv, created, err := svc.Invite(ctx, other, org1, in)
	if err != nil || !created || iv.Email != "invitee@example.invalid" || iv.Status != "PENDING" {
		t.Fatal("invite", iv, created, err)
	}
	if _, created, err = svc.Invite(ctx, other, org1, in); err != nil || created {
		t.Fatal("replay must not duplicate", created, err)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, inv1).Scan(&n)
	if n != 1 {
		t.Fatal("replay duplicated email event", n)
	}
	var hash string
	pool.QueryRow(ctx, `SELECT token_hash FROM organization_invitations WHERE id=$1`, inv1).Scan(&hash)
	token := tokenOf(inv1)
	if hash != application.HashInvitationToken(token) || hash == token {
		t.Fatal("token hash mismatch")
	}
	_, _, err = svc.Invite(ctx, other, org1, application.InvitationInput{ID: "30000000-0000-0000-0000-000000000002", Email: "invitee@example.invalid", Role: "CHEF", LocationIDs: []string{l1}})
	is(err, domain.ErrInvitationPending, "second pending")
	_, _, err = svc.Invite(ctx, other, org1, application.InvitationInput{ID: "30000000-0000-0000-0000-000000000003", Email: "WORKER@example.invalid", Role: "CHEF", LocationIDs: []string{l1}})
	is(err, domain.ErrMemberExists, "existing member")
	_, _, err = svc.Invite(ctx, other, org1, application.InvitationInput{ID: "30000000-0000-0000-0000-000000000004", Email: "z@example.invalid", Role: "WORKER"})
	if !errors.As(err, &validation) {
		t.Fatal("worker invitation without locations", err)
	}
	page, err := svc.InvitationsPage(ctx, other, org1, "PENDING", pagination.Request{Size: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("invitation page", page, err)
	}
	if _, err = svc.InvitationsPage(ctx, "ob", org1, "", pagination.Request{Size: 10}); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("foreign admin lists invitations", err)
	}

	// Email distinto, token inexistente.
	_, err = svc.AcceptInvitation(ctx, "outsider", "outsider@example.invalid", token)
	is(err, domain.ErrInvitationEmail, "different email")
	_, err = svc.AcceptInvitation(ctx, "invitee", "otro@example.invalid", token)
	is(err, domain.ErrInvitationEmail, "token email differs from invitation")
	_, err = svc.AcceptInvitation(ctx, "invitee", "invitee@example.invalid", "short")
	is(err, domain.ErrInvitationNotFound, "malformed token")
	tk, _, _ := application.NewInvitationToken()
	_, err = svc.AcceptInvitation(ctx, "invitee", "invitee@example.invalid", tk)
	is(err, domain.ErrInvitationNotFound, "unknown token")

	// Vista previa pública: invitación vigente (solo datos mínimos) y errores como aceptar.
	pv, err := svc.PreviewInvitation(ctx, token)
	if err != nil || pv.OrganizationName != "Uno SAC" || pv.Role != "WORKER" || pv.Email != "invitee@example.invalid" || pv.Status != domain.InvitationPending || !pv.ExpiresAt.After(time.Now()) {
		t.Fatal("preview vigente", pv, err)
	}
	_, err = svc.PreviewInvitation(ctx, "short")
	is(err, domain.ErrInvitationNotFound, "preview malformed token")
	_, err = svc.PreviewInvitation(ctx, tk)
	is(err, domain.ErrInvitationNotFound, "preview unknown token")

	// Reenviar: espera mínima, nuevo token invalida el anterior.
	_, err = svc.ResendInvitation(ctx, other, org1, inv1)
	is(err, domain.ErrResendTooSoon, "resend too soon")
	exec(`UPDATE organization_invitations SET last_sent_at=NOW()-interval '5 minutes' WHERE id=$1`, inv1)
	rs, err := svc.ResendInvitation(ctx, other, org1, inv1)
	if err != nil || rs.SendCount != 2 {
		t.Fatal("resend", rs, err)
	}
	newToken := tokenOf(inv1)
	if newToken == token {
		t.Fatal("resend reused the token")
	}
	_, err = svc.AcceptInvitation(ctx, "invitee", "invitee@example.invalid", token)
	is(err, domain.ErrInvitationNotFound, "old token after resend")
	_, err = svc.PreviewInvitation(ctx, token)
	is(err, domain.ErrInvitationNotFound, "preview old token after resend")
	pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND payload ? 'token'`, inv1).Scan(&n)
	if n != 1 {
		t.Fatal("old token left in the outbox", n)
	}

	// Vencida: no se acepta; reenviar la reactiva.
	exec(`UPDATE organization_invitations SET expires_at=NOW()-interval '1 minute' WHERE id=$1`, inv1)
	_, err = svc.AcceptInvitation(ctx, "invitee", "invitee@example.invalid", newToken)
	is(err, domain.ErrInvitationExpired, "expired")
	_, err = svc.PreviewInvitation(ctx, newToken)
	is(err, domain.ErrInvitationExpired, "preview expired")
	exp, _ := svc.InvitationsPage(ctx, other, org1, "EXPIRED", pagination.Request{Size: 10})
	if len(exp.Items) != 1 {
		t.Fatal("expired status not derived", exp)
	}
	exec(`UPDATE organization_invitations SET last_sent_at=NOW()-interval '5 minutes' WHERE id=$1`, inv1)
	if _, err = svc.ResendInvitation(ctx, other, org1, inv1); err != nil {
		t.Fatal("resend expired", err)
	}
	newToken = tokenOf(inv1)

	// Aceptación válida y reutilización.
	got, err := svc.AcceptInvitation(ctx, "invitee", "INVITEE@example.invalid", newToken)
	if err != nil || got.OrganizationID != org1 || got.Role != "WORKER" || len(got.LocationIDs) != 1 {
		t.Fatal("accept", got, err)
	}
	if _, err = svc.Workspace(ctx, "invitee", org1); err != nil {
		t.Fatal("invitee cannot operate", err)
	}
	_, err = svc.AcceptInvitation(ctx, "invitee", "invitee@example.invalid", newToken)
	is(err, domain.ErrInvitationUsed, "reuse")
	_, err = svc.PreviewInvitation(ctx, newToken)
	is(err, domain.ErrInvitationUsed, "preview accepted")
	_, err = svc.RevokeInvitation(ctx, other, org1, inv1)
	is(err, domain.ErrInvitationNotOpen, "revoke accepted")

	// Revocada.
	const inv2 = "30000000-0000-0000-0000-000000000005"
	if _, _, err = svc.Invite(ctx, other, org1, application.InvitationInput{ID: inv2, Email: "outsider@example.invalid", Role: "ADMIN"}); err != nil {
		t.Fatal(err)
	}
	revokedToken := tokenOf(inv2)
	if _, err = svc.RevokeInvitation(ctx, other, org1, inv2); err != nil {
		t.Fatal(err)
	}
	_, err = svc.AcceptInvitation(ctx, "outsider", "outsider@example.invalid", revokedToken)
	is(err, domain.ErrInvitationRevoked, "revoked")
	_, err = svc.PreviewInvitation(ctx, revokedToken)
	is(err, domain.ErrInvitationRevoked, "preview revoked")
	_, err = svc.RevokeInvitation(ctx, "ob", org1, inv2)
	is(err, domain.ErrDenied, "foreign admin revokes")

	// Dispositivos: solo los propios.
	devs, err := svc.MyDevices(ctx, "worker", pagination.Request{Size: 10})
	if err != nil || len(devs.Items) != 1 || devs.Items[0].ID != "40000000-0000-0000-0000-000000000002" {
		t.Fatal("own devices", devs, err)
	}
	adminDevs, err := svc.MyDevices(ctx, other, pagination.Request{Size: 10})
	if err != nil || len(adminDevs.Items) != 0 {
		t.Fatal("admin sees other people's devices", adminDevs, err)
	}
	_, err = svc.RevokeMyDevice(ctx, other, "40000000-0000-0000-0000-000000000002")
	is(err, domain.ErrNotFound, "admin revokes another user's device")
	d, err := svc.RevokeMyDevice(ctx, "worker", "40000000-0000-0000-0000-000000000002")
	if err != nil || d.Status != "INACTIVE" {
		t.Fatal("revoke device", d, err)
	}
	if _, err = svc.RevokeMyDevice(ctx, "worker", "40000000-0000-0000-0000-000000000002"); err != nil {
		t.Fatal("revoke is not idempotent", err)
	}
	var tok *string
	pool.QueryRow(ctx, `SELECT push_token FROM mobile_devices WHERE id='40000000-0000-0000-0000-000000000002'`).Scan(&tok)
	if tok != nil {
		t.Fatal("push token kept")
	}
	devs, _ = svc.MyDevices(ctx, "worker", pagination.Request{Size: 10})
	if len(devs.Items) != 0 {
		t.Fatal("revoked device still listed")
	}
}
