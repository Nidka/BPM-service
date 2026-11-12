package researchhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/research/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/research/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	orgA = "10000000-0000-0000-0000-00000000000a"
	orgB = "10000000-0000-0000-0000-00000000000b"
)

type tokenVerifier struct{}

func (tokenVerifier) VerifyIDToken(_ context.Context, raw string) (contracts.Identity, error) {
	return contracts.Identity{UID: raw, EmailVerified: true}, nil
}

// database crea un esquema aislado en bpm_test y aplica V1 y V9. Se omite sin TEST_DATABASE_URL.
func database(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires isolated bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{fmt.Sprintf("research_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = postgres.New(pool).RequireSchema(ctx); err == nil {
		t.Skip("bpm_test public ya contiene V9; se requiere base limpia en el esquema")
	}
	for _, name := range []string{"V1__baseline.sql", "V9__research_cap.sql"} {
		sql, err := os.ReadFile("../../../../../migraciones/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(name, err)
		}
	}
	if err = postgres.New(pool).RequireSchema(ctx); err != nil {
		t.Fatal("no reconoce V9", err)
	}
	return ctx, pool
}

type client struct {
	t      *testing.T
	router http.Handler
}

func (c client) do(method, path, user string, body any) (int, map[string]any, string) {
	c.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+user)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	c.router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func (c client) expect(want int, code, method, path, user string, body any) map[string]any {
	c.t.Helper()
	got, out, raw := c.do(method, path, user, body)
	if got != want {
		c.t.Fatalf("%s %s como %s: HTTP %d, se esperaba %d: %s", method, path, user, got, want, raw)
	}
	if code != "" {
		errs, _ := out["errors"].([]any)
		if len(errs) == 0 || errs[0].(map[string]any)["code"] != code {
			c.t.Fatalf("%s %s como %s: código %v, se esperaba %s", method, path, user, errs, code)
		}
	}
	return out
}

func obj(v any) map[string]any { return v.(map[string]any) }

func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, sql := range []string{
		fmt.Sprintf(`INSERT INTO organizations(id,legal_name,business_type) VALUES('%s','Empresa A','RESTAURANT'),('%s','Empresa B','RESTAURANT')`, orgA, orgB),
		`INSERT INTO application_users(id,email,display_name,platform_role) VALUES
			('uid-admin','admin@example.invalid','Admin Empresa','USER'),('uid-worker','worker@example.invalid','Worker Uno','USER'),
			('uid-worker2','worker2@example.invalid','Worker Dos','USER'),('uid-bworker','bworker@example.invalid','Worker B','USER'),
			('uid-res','res@example.invalid','Investigadora','USER'),('uid-super','super@example.invalid','Super','SUPER_ADMIN')`,
		fmt.Sprintf(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES('%s','uid-admin','ADMIN'),('%s','uid-worker','WORKER'),('%s','uid-worker2','WORKER'),('%s','uid-bworker','WORKER')`, orgA, orgA, orgA, orgB),
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
}

func uuidN(n int) string { return fmt.Sprintf("20000000-0000-4000-8000-%012d", n) }

// answers arma las 26 respuestas: opción mc para MC (la correcta es 1) y scale para las escalas.
func answers(survey map[string]any, mc, scale int, wrongFirst bool) []map[string]any {
	var out []map[string]any
	for i, q := range survey["questions"].([]any) {
		v := scale
		if obj(q)["type"] == "MULTIPLE_CHOICE" {
			v = mc
			if wrongFirst && i == 0 {
				v = 0
			}
		}
		out = append(out, map[string]any{"questionId": obj(q)["id"], "value": v})
	}
	return out
}

func TestResearchEndToEnd(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	router := chi.NewRouter()
	New(application.New(postgres.New(pool)), nil).Register(router, middleware.Authenticate(tokenVerifier{}))
	c := client{t: t, router: router}

	// Un ADMIN de empresa no tiene acceso de investigación.
	for _, p := range []string{"/research/instruments", "/research/results", "/research/exports/responses.csv", "/research/needs", "/research/organizations", "/research/consent-documents"} {
		c.expect(403, "RESEARCH_ACCESS_DENIED", "GET", p, "uid-admin", nil)
	}
	c.expect(403, "RESEARCH_ACCESS_DENIED", "POST", "/research/needs", "uid-admin", map[string]any{"organizationId": orgA, "category": "TRAINING", "description": "x"})
	c.expect(403, "RESEARCH_ACCESS_DENIED", "GET", "/research/access-grants", "uid-admin", nil)
	// Tampoco la tiene SUPER_ADMIN sin concesión explícita.
	c.expect(403, "RESEARCH_ACCESS_DENIED", "GET", "/research/instruments", "uid-super", nil)
	// Solo SUPER_ADMIN concede el acceso.
	c.expect(403, "RESEARCH_ACCESS_DENIED", "POST", "/research/access-grants", "uid-admin", map[string]any{"email": "res@example.invalid"})
	c.expect(404, "RESEARCH_USER_NOT_FOUND", "POST", "/research/access-grants", "uid-super", map[string]any{"email": "nadie@example.invalid"})
	c.expect(200, "", "POST", "/research/access-grants", "uid-super", map[string]any{"email": "RES@example.invalid"})
	c.expect(403, "RESEARCH_ACCESS_DENIED", "POST", "/research/access-grants", "uid-res", map[string]any{"email": "worker@example.invalid"})
	acc := obj(c.expect(200, "", "GET", "/research/access", "uid-res", nil)["data"])
	if acc["canResearch"] != true || acc["canManageGrants"] != false {
		t.Fatalf("access: %v", acc)
	}
	if obj(c.expect(200, "", "GET", "/research/access", "uid-admin", nil)["data"])["canResearch"] != false {
		t.Fatal("el admin de empresa no es investigador")
	}

	// Importar el cuestionario CAP real como borrador.
	raw, err := os.ReadFile("../../../../../docs/research/cap-instrument.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	imported := obj(c.expect(201, "", "POST", "/research/instruments/import", "uid-res", doc)["data"])
	instrument := obj(imported["instrument"])["id"].(string)
	if n := len(imported["questions"].([]any)); n != 26 {
		t.Fatalf("preguntas importadas: %d", n)
	}
	c.expect(409, "RESEARCH_INSTRUMENT_CODE_TAKEN", "POST", "/research/instruments/import", "uid-res", doc)
	// Sin respuestas correctas no se publica.
	c.expect(422, "RESEARCH_INSTRUMENT_INCOMPLETE", "POST", "/research/instruments/"+instrument+"/publish", "uid-res", nil)
	// Composición CAP incorrecta (7 opción múltiple) no se publica.
	draft := obj(c.expect(200, "", "GET", "/research/instruments/"+instrument+"/draft", "uid-res", nil)["data"])
	var qs, short []map[string]any
	for _, q := range draft["questions"].([]any) {
		m := obj(q)
		item := map[string]any{"type": m["type"], "text": m["text"], "options": m["options"], "correctOption": nil}
		if m["type"] == "MULTIPLE_CHOICE" {
			item["correctOption"] = 1
		}
		qs = append(qs, item)
		if len(short) < 7 || m["type"] != "MULTIPLE_CHOICE" {
			short = append(short, item)
		}
	}
	version := obj(draft["version"])
	saved := obj(c.expect(200, "", "PUT", "/research/instruments/"+instrument+"/draft", "uid-res", map[string]any{"rowVersion": version["rowVersion"], "questions": short})["data"])
	c.expect(409, "RESEARCH_DRAFT_CONFLICT", "PUT", "/research/instruments/"+instrument+"/draft", "uid-res", map[string]any{"rowVersion": version["rowVersion"], "questions": qs})
	c.expect(422, "RESEARCH_INSTRUMENT_INCOMPLETE", "POST", "/research/instruments/"+instrument+"/publish", "uid-res", nil)
	c.expect(200, "", "PUT", "/research/instruments/"+instrument+"/draft", "uid-res", map[string]any{"rowVersion": obj(saved["version"])["rowVersion"], "questions": qs})
	published := obj(c.expect(200, "", "POST", "/research/instruments/"+instrument+"/publish", "uid-res", nil)["data"])
	versionID := obj(published["version"])["id"].(string)
	// Publicada es inmutable: ya no hay borrador y la base rechaza cambios directos.
	c.expect(404, "RESEARCH_DRAFT_NOT_FOUND", "GET", "/research/instruments/"+instrument+"/draft", "uid-res", nil)
	if _, err = pool.Exec(ctx, `UPDATE survey_questions SET question_text='x' WHERE instrument_version_id=$1`, versionID); err == nil {
		t.Fatal("la base permitió editar una pregunta publicada")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM survey_questions WHERE instrument_version_id=$1`, versionID); err == nil {
		t.Fatal("la base permitió borrar preguntas publicadas")
	}

	// Consentimiento: sin documento no hay participación.
	c.expect(409, "RESEARCH_CONSENT_DOCUMENT_UNAVAILABLE", "POST", "/organizations/"+orgA+"/research/consent", "uid-worker", map[string]any{"documentVersion": "v1", "accepted": true})
	c.expect(403, "ACCESS_DENIED", "GET", "/organizations/"+orgB+"/research/participation", "uid-worker", nil)
	c.expect(403, "RESEARCH_ACCESS_DENIED", "POST", "/research/consent-documents", "uid-worker", map[string]any{"version": "v1", "title": "t", "text": "x", "responsibleInvestigator": "Dra. Ana"})
	c.expect(201, "", "POST", "/research/consent-documents", "uid-res", map[string]any{"version": "v1", "title": "Consentimiento", "text": "Texto del consentimiento", "responsibleInvestigator": "Dra. Ana"})
	c.expect(409, "RESEARCH_CONSENT_VERSION_TAKEN", "POST", "/research/consent-documents", "uid-res", map[string]any{"version": "v1", "title": "t", "text": "x", "responsibleInvestigator": "i"})

	part := obj(c.expect(200, "", "GET", "/organizations/"+orgA+"/research/participation", "uid-worker", nil)["data"])
	if part["participant"] != nil || part["survey"] != nil || obj(part["consentDocument"])["responsibleInvestigator"] != "Dra. Ana" {
		t.Fatalf("participación inicial: %v", part)
	}
	// Sin consentimiento no se puede responder.
	submit := func(user, org, id, op, ver, moment string, ans []map[string]any) (int, map[string]any, string) {
		return c.do("POST", "/organizations/"+org+"/research/submissions", user, map[string]any{"id": id, "clientOperationId": op, "instrumentVersionId": ver,
			"moment": moment, "clientCreatedAt": time.Now().UTC().Format(time.RFC3339), "answers": ans})
	}
	fakeAns := []map[string]any{{"questionId": uuidN(99), "value": 1}}
	if code, out, _ := submit("uid-worker", orgA, uuidN(1), uuidN(2), versionID, "PRETEST", fakeAns); code != 403 || obj(out["errors"].([]any)[0])["code"] != "RESEARCH_CONSENT_REQUIRED" {
		t.Fatalf("sin consentimiento: %d %v", code, out)
	}
	c.expect(409, "RESEARCH_CONSENT_VERSION_OUTDATED", "POST", "/organizations/"+orgA+"/research/consent", "uid-worker", map[string]any{"documentVersion": "v0", "accepted": true})
	c.expect(404, "RESEARCH_PARTICIPANT_NOT_FOUND", "POST", "/organizations/"+orgA+"/research/consent", "uid-worker", map[string]any{"documentVersion": "v1", "accepted": false})
	accepted := obj(c.expect(200, "", "POST", "/organizations/"+orgA+"/research/consent", "uid-worker", map[string]any{"documentVersion": "v1", "accepted": true})["data"])
	code := obj(accepted["participant"])["code"].(string)
	if !strings.HasPrefix(code, "P-") || strings.Contains(code, "worker") || accepted["consentValid"] != true {
		t.Fatalf("participante: %v", accepted)
	}
	// Repetir la aceptación es idempotente y conserva el código.
	again := obj(c.expect(200, "", "POST", "/organizations/"+orgA+"/research/consent", "uid-worker", map[string]any{"documentVersion": "v1", "accepted": true})["data"])
	if obj(again["participant"])["code"] != code {
		t.Fatal("el código seudónimo cambió")
	}
	var consentRows int
	pool.QueryRow(ctx, `SELECT count(*) FROM consent_records`).Scan(&consentRows)
	if consentRows != 1 {
		t.Fatalf("registros de consentimiento: %d", consentRows)
	}

	// La encuesta que ve el participante no revela respuestas correctas.
	_, _, body := c.do("GET", "/organizations/"+orgA+"/research/participation", "uid-worker", nil)
	if strings.Contains(strings.ToLower(body), "correct") {
		t.Fatal("la vista del participante filtra respuestas correctas")
	}
	survey := obj(obj(c.expect(200, "", "GET", "/organizations/"+orgA+"/research/participation", "uid-worker", nil)["data"])["survey"])
	if survey["instrumentVersionId"] != versionID || len(survey["questions"].([]any)) != 26 {
		t.Fatalf("encuesta: %v", survey["instrumentVersionId"])
	}

	// PRETEST: 7 de 8 correctas, actitud 4, práctica 3.
	pre := answers(survey, 1, 4, true)
	for _, a := range pre[16:] {
		a["value"] = 3
	}
	res := c.expect(201, "", "POST", "/organizations/"+orgA+"/research/submissions", "uid-worker", map[string]any{"id": uuidN(1), "clientOperationId": uuidN(2), "instrumentVersionId": versionID,
		"moment": "PRETEST", "clientCreatedAt": time.Now().UTC().Format(time.RFC3339), "answers": pre})
	scores := obj(obj(res["data"])["scores"])
	if scores["knowledgeCorrect"] != float64(7) || scores["knowledgePercent"] != 87.5 || scores["attitudeMean"] != float64(4) || scores["practiceMean"] != float64(3) {
		t.Fatalf("puntajes: %v", scores)
	}
	// Repetición idéntica = replay 200; mismo id con otro contenido = conflicto; otro id mismo momento = duplicado.
	clientAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	first := map[string]any{"id": uuidN(11), "clientOperationId": uuidN(12), "instrumentVersionId": versionID, "moment": "POSTTEST", "clientCreatedAt": clientAt, "answers": answers(survey, 1, 5, false)}
	c.expect(201, "", "POST", "/organizations/"+orgA+"/research/submissions", "uid-worker", first)
	c.expect(200, "", "POST", "/organizations/"+orgA+"/research/submissions", "uid-worker", first)
	changed := map[string]any{"id": uuidN(11), "clientOperationId": uuidN(12), "instrumentVersionId": versionID, "moment": "POSTTEST", "clientCreatedAt": clientAt, "answers": answers(survey, 1, 2, false)}
	c.expect(409, "IDEMPOTENCY_CONFLICT", "POST", "/organizations/"+orgA+"/research/submissions", "uid-worker", changed)
	if code, _, _ := submit("uid-worker", orgA, uuidN(21), uuidN(22), versionID, "PRETEST", answers(survey, 1, 3, false)); code != 409 {
		t.Fatalf("PRETEST duplicado: %d", code)
	}
	if code, _, _ := submit("uid-worker", orgA, uuidN(23), uuidN(24), versionID, "POSTTEST", answers(survey, 1, 3, false)); code != 409 {
		t.Fatalf("POSTTEST duplicado: %d", code)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM survey_submissions`).Scan(&n)
	if n != 2 {
		t.Fatalf("entregas: %d", n)
	}

	// Versión incorrecta.
	c.expect(200, "", "POST", "/organizations/"+orgA+"/research/consent", "uid-worker2", map[string]any{"documentVersion": "v1", "accepted": true})
	if code, out, _ := submit("uid-worker2", orgA, uuidN(31), uuidN(32), uuidN(77), "PRETEST", answers(survey, 1, 3, false)); code != 409 || obj(out["errors"].([]any)[0])["code"] != "RESEARCH_VERSION_OUTDATED" {
		t.Fatalf("versión incorrecta: %d %v", code, out)
	}
	// Respuestas inválidas: faltantes.
	if code, out, _ := submit("uid-worker2", orgA, uuidN(33), uuidN(34), versionID, "PRETEST", answers(survey, 1, 3, false)[:25]); code != 422 || obj(out["errors"].([]any)[0])["code"] != "RESEARCH_ANSWERS_INVALID" {
		t.Fatalf("respuestas faltantes: %d %v", code, out)
	}
	// Consentimiento revocado: no puede responder y sus datos salen de resultados.
	revoked := obj(c.expect(200, "", "POST", "/organizations/"+orgA+"/research/consent", "uid-worker2", map[string]any{"documentVersion": "v1", "accepted": false})["data"])
	if revoked["consentValid"] != false || obj(revoked["participant"])["status"] != "WITHDRAWN" || revoked["survey"] != nil {
		t.Fatalf("revocación: %v", revoked)
	}
	if code, out, _ := submit("uid-worker2", orgA, uuidN(35), uuidN(36), versionID, "PRETEST", answers(survey, 1, 3, false)); code != 403 || obj(out["errors"].([]any)[0])["code"] != "RESEARCH_CONSENT_REQUIRED" {
		t.Fatalf("revocado: %d %v", code, out)
	}
	// Empresa B responde un PRETEST.
	c.expect(200, "", "POST", "/organizations/"+orgB+"/research/consent", "uid-bworker", map[string]any{"documentVersion": "v1", "accepted": true})
	if code, _, raw := submit("uid-bworker", orgB, uuidN(41), uuidN(42), versionID, "PRETEST", answers(survey, 0, 5, false)); code != 201 {
		t.Fatalf("empresa B: %d %s", code, raw)
	}
	// El tenant sale de la membresía: un miembro de A no responde en B.
	if code, _, _ := submit("uid-worker", orgB, uuidN(51), uuidN(52), versionID, "PRETEST", answers(survey, 1, 3, false)); code != 403 {
		t.Fatalf("tenant ajeno: %d", code)
	}

	// Resultados consolidados por empresa solo para investigadores.
	c.expect(403, "RESEARCH_ACCESS_DENIED", "GET", "/research/results", "uid-admin", nil)
	results := c.expect(200, "", "GET", "/research/results?pageSize=1", "uid-res", nil)
	items := results["data"].([]any)
	token := obj(results["pagination"])["nextPageToken"].(string)
	if len(items) != 1 || token == "" {
		t.Fatalf("paginación de resultados: %v", results)
	}
	second := c.expect(200, "", "GET", "/research/results?pageSize=1&pageToken="+token, "uid-res", nil)
	items = append(items, second["data"].([]any)...)
	byOrg := map[string]map[string]any{}
	for _, it := range items {
		byOrg[obj(it)["organizationId"].(string)] = obj(it)
	}
	a := byOrg[orgA]
	if a["participants"] != float64(1) || obj(a["pretest"])["n"] != float64(1) || obj(a["posttest"])["n"] != float64(1) || obj(a["paired"])["n"] != float64(1) {
		t.Fatalf("resultados A (el participante retirado no cuenta): %v", a)
	}
	if obj(a["pretest"])["knowledgeMean"] != 87.5 || obj(a["posttest"])["knowledgeMean"] != float64(100) || obj(a["paired"])["knowledgeDelta"] != 12.5 {
		t.Fatalf("medias A: %v", a)
	}
	if obj(byOrg[orgB]["posttest"])["n"] != float64(0) || obj(byOrg[orgB]["posttest"])["knowledgeMean"] != nil {
		t.Fatalf("resultados B: %v", byOrg[orgB])
	}
	c.expect(400, "INVALID_PAGINATION", "GET", "/research/results?pageToken=basura!", "uid-res", nil)

	// Exportación CSV anonimizada.
	c.expect(403, "RESEARCH_ACCESS_DENIED", "GET", "/research/exports/responses.csv", "uid-admin", nil)
	_, _, csvBody := c.do("GET", "/research/exports/responses.csv", "uid-res", nil)
	lines := strings.Split(strings.TrimSpace(csvBody), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "participantCode,organizationId,moment,submittedDate,instrumentVersion,k1,") {
		t.Fatalf("CSV inesperado (%d líneas): %s", len(lines), csvBody)
	}
	for _, forbidden := range []string{"example.invalid", "uid-", "Worker", "Admin", "Empresa A", "Investigadora"} {
		if strings.Contains(csvBody, forbidden) {
			t.Fatalf("el CSV contiene datos identificables (%s)", forbidden)
		}
	}
	if !strings.Contains(csvBody, code) {
		t.Fatal("el CSV debe usar el código seudónimo")
	}
	_, _, filtered := c.do("GET", "/research/exports/responses.csv?organizationId="+orgB, "uid-res", nil)
	if len(strings.Split(strings.TrimSpace(filtered), "\n")) != 2 {
		t.Fatalf("filtro por empresa: %s", filtered)
	}
	c.expect(404, "RESEARCH_ORGANIZATION_NOT_FOUND", "GET", "/research/exports/responses.csv?organizationId=nope", "uid-res", nil)
	var audited int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='research.export.csv' AND actor_user_id='uid-res'`).Scan(&audited)
	if audited != 2 {
		t.Fatalf("auditoría de exportaciones: %d", audited)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action IN ('research.results.read','research.instrument.listed') AND actor_user_id='uid-res'`).Scan(&audited)
	if audited == 0 {
		t.Fatal("los accesos de investigación no se auditan")
	}

	// RF-50: necesidades detectadas.
	c.expect(404, "RESEARCH_ORGANIZATION_NOT_FOUND", "POST", "/research/needs", "uid-res", map[string]any{"organizationId": "nope", "category": "TRAINING", "description": "x"})
	c.expect(422, "VALIDATION_ERROR", "POST", "/research/needs", "uid-res", map[string]any{"organizationId": orgA, "category": "NOPE", "description": "x"})
	for i := 0; i < 3; i++ {
		c.expect(201, "", "POST", "/research/needs", "uid-res", map[string]any{"organizationId": orgA, "category": "TRAINING", "description": fmt.Sprintf("Necesidad %d", i),
			"detectedAt": time.Now().UTC().Add(-time.Duration(i) * time.Hour).Format(time.RFC3339)})
	}
	c.expect(201, "", "POST", "/research/needs", "uid-res", map[string]any{"organizationId": orgB, "category": "PROCESS", "description": "Otra empresa"})
	page := c.expect(200, "", "GET", "/research/needs?organizationId="+orgA+"&pageSize=2", "uid-res", nil)
	if len(page["data"].([]any)) != 2 || obj(page["data"].([]any)[0])["description"] != "Necesidad 0" {
		t.Fatalf("necesidades: %v", page)
	}
	rest := c.expect(200, "", "GET", "/research/needs?organizationId="+orgA+"&pageSize=2&pageToken="+obj(page["pagination"])["nextPageToken"].(string), "uid-res", nil)
	if len(rest["data"].([]any)) != 1 || obj(rest["pagination"])["nextPageToken"] != "" {
		t.Fatalf("segunda página: %v", rest)
	}
	c.expect(403, "RESEARCH_ACCESS_DENIED", "GET", "/research/needs", "uid-admin", nil)

	// Revocar la concesión corta el acceso.
	c.expect(204, "", "DELETE", "/research/access-grants/uid-res", "uid-super", nil)
	c.expect(403, "RESEARCH_ACCESS_DENIED", "GET", "/research/results", "uid-res", nil)
	c.expect(404, "RESEARCH_GRANT_NOT_FOUND", "DELETE", "/research/access-grants/uid-nadie", "uid-super", nil)
}
