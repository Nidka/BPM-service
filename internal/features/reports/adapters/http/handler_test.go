package reportshttp

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/reports/adapters/pdf"
	"github.com/Nidka/BPM-service/internal/features/reports/application"
	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/go-chi/chi/v5"
)

type verifier struct{}

func (verifier) VerifyIDToken(_ context.Context, raw string) (contracts.Identity, error) {
	return contracts.Identity{UID: raw, EmailVerified: true}, nil
}

// fakeStore implementa solo lo que usan estas pruebas; el resto entra en pánico si se invoca.
type fakeStore struct {
	application.Store
	roles map[string]string
}

func (f fakeStore) Actor(_ context.Context, user, org string) (domain.Actor, error) {
	if role, ok := f.roles[org+"|"+user]; ok {
		return domain.Actor{ID: user, Role: role}, nil
	}
	return domain.Actor{}, domain.ErrDenied
}
func (fakeStore) LocationNames(context.Context, domain.Scope) (map[string]string, error) {
	return map[string]string{}, nil
}
func (fakeStore) Audit(context.Context, string, string, string, string, string) error { return nil }
func (fakeStore) OrganizationName(context.Context, string) (string, error) {
	return "Empresa Demo", nil
}
func (fakeStore) CountExportRows(context.Context, domain.Scope, int) (int, error) { return 2, nil }
func (fakeStore) StreamExportRows(_ context.Context, _ domain.Scope, fn func(domain.ExportRow) error) error {
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	for _, name := range []string{"=EVIL()", "Sede, \"con\" comas"} {
		if err := fn(domain.ExportRow{RunID: "r", LocationID: "l", LocationName: name, TemplateName: "T", Area: "A", LocalDate: at, SubmittedAt: at, Zone: "America/Lima", TemplateVersion: 1, CompliancePercent: 100}); err != nil {
			return err
		}
	}
	return nil
}
func (fakeStore) ComplianceRows(context.Context, domain.Scope) ([]domain.DailyRow, error) {
	return []domain.DailyRow{{LocationID: "l", LocationName: "S", Day: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Runs: 2, Sum: 150}}, nil
}
func (fakeStore) Settings(context.Context, string) (domain.Settings, error) {
	return domain.DefaultSettings(), nil
}
func (fakeStore) LockCertificates(context.Context, string) error { return nil }
func (fakeStore) FindCertificate(context.Context, string, domain.Period) (*domain.Certificate, error) {
	return nil, nil
}

type fakeWork struct{ store fakeStore }

func (w fakeWork) Within(_ context.Context, fn func(application.Store) error) error {
	return fn(w.store)
}

type fakeVerifier struct{}

func (fakeVerifier) CertificateByCode(context.Context, string) (domain.PublicCertificate, error) {
	return domain.PublicCertificate{}, domain.ErrCertificateNotFound
}

func router() http.Handler {
	work := fakeWork{fakeStore{roles: map[string]string{"org|admin": "ADMIN", "org|chef": "CHEF", "org|worker": "WORKER"}}}
	r := chi.NewRouter()
	New(application.New(work, fakeVerifier{}), nil).WithPDF(pdf.Render).Register(r, middleware.Authenticate(verifier{}))
	return r
}

func call(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router().ServeHTTP(rec, req)
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Errors []struct{ Code string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || len(e.Errors) == 0 {
		t.Fatalf("sin error uniforme: %s", rec.Body.String())
	}
	return e.Errors[0].Code
}

func TestContratoHTTP(t *testing.T) {
	const base = "/organizations/org/reports"
	period := "?from=2026-01-01&to=2026-01-31"
	for _, c := range []struct {
		name, method, path, token, body, want string
		status                                int
	}{
		{"sin token", "GET", base + "/compliance" + period, "", "", "UNAUTHENTICATED", 401},
		{"worker", "GET", base + "/compliance" + period, "worker", "", "ACCESS_DENIED", 403},
		{"worker exporta", "GET", base + "/compliance/export.csv" + period, "worker", "", "ACCESS_DENIED", 403},
		{"pdf sin token", "GET", base + "/compliance/export.pdf" + period, "", "", "UNAUTHENTICATED", 401},
		{"worker exporta pdf", "GET", base + "/compliance/export.pdf" + period, "worker", "", "ACCESS_DENIED", 403},
		{"pdf de otra empresa", "GET", "/organizations/otra/reports/compliance/export.pdf" + period, "admin", "", "ACCESS_DENIED", 403},
		{"pdf con parámetro desconocido", "GET", base + "/compliance/export.pdf" + period + "&x=1", "admin", "", "INVALID_REQUEST", 400},
		{"pdf sin fechas", "GET", base + "/compliance/export.pdf", "admin", "", "VALIDATION_ERROR", 422},
		{"pdf con sede inválida", "GET", base + "/compliance/export.pdf" + period + "&locationId=x", "admin", "", "REPORT_LOCATION_NOT_FOUND", 404},
		{"chef sin constancias", "POST", base + "/certificates", "chef", `{"from":"2026-01-01","to":"2026-01-31"}`, "ACCESS_DENIED", 403},
		{"otra empresa", "GET", "/organizations/otra/reports/kpis", "admin", "", "ACCESS_DENIED", 403},
		{"parámetro desconocido", "GET", base + "/compliance" + period + "&x=1", "admin", "", "INVALID_REQUEST", 400},
		{"parámetro repetido", "GET", base + "/compliance?from=2026-01-01&from=2026-01-02&to=2026-01-03", "admin", "", "INVALID_REQUEST", 400},
		{"fechas faltantes", "GET", base + "/compliance", "admin", "", "VALIDATION_ERROR", 422},
		{"período largo", "GET", base + "/compliance?from=2026-01-01&to=2026-12-31", "admin", "", "VALIDATION_ERROR", 422},
		{"sede inválida", "GET", base + "/compliance" + period + "&locationId=x", "admin", "", "REPORT_LOCATION_NOT_FOUND", 404},
		{"paginación inválida", "GET", base + "/training/people?pageSize=0", "admin", "", "INVALID_PAGINATION", 400},
		{"ajustes sin campos", "PUT", base + "/settings", "admin", `{}`, "VALIDATION_ERROR", 422},
		{"ajustes con campo extra", "PUT", base + "/settings", "admin", `{"weeklyEmailEnabled":true,"complianceThreshold":80,"x":1}`, "INVALID_REQUEST", 400},
		{"umbral fuera de rango", "PUT", base + "/settings", "admin", `{"weeklyEmailEnabled":true,"complianceThreshold":0}`, "VALIDATION_ERROR", 422},
		{"constancia con período abierto", "POST", base + "/certificates", "admin", `{"from":"2026-01-01","to":"2999-01-01"}`, "VALIDATION_ERROR", 422},
		{"verificación desconocida", "GET", "/compliance-certificates/AAAAAAAAAAAAAAAAAAAAAAAA", "", "", "CERTIFICATE_NOT_FOUND", 404},
		{"verificación mal formada", "GET", "/compliance-certificates/xyz", "", "", "CERTIFICATE_NOT_FOUND", 404},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := call(c.method, c.path, c.token, c.body)
			if rec.Code != c.status || code(t, rec) != c.want {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
		})
	}
	t.Run("umbral no alcanzado", func(t *testing.T) {
		// 75 % < 80 %: el servidor rechaza y no emite.
		rec := call("POST", base+"/certificates", "admin", `{"from":"2026-01-01","to":"2026-01-31"}`)
		if rec.Code != 422 || code(t, rec) != "REPORT_THRESHOLD_NOT_MET" || !strings.Contains(rec.Body.String(), `"averageCompliance":"75.00"`) {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("cumplimiento", func(t *testing.T) {
		rec := call("GET", base+"/compliance"+period, "chef", "")
		var out struct {
			Data struct {
				Runs              int      `json:"runs"`
				AverageCompliance *float64 `json:"averageCompliance"`
			} `json:"data"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Data.Runs != 2 || *out.Data.AverageCompliance != 75 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("csv", func(t *testing.T) {
		rec := call("GET", base+"/compliance/export.csv"+period, "chef", "")
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("cabeceras: %d %v", rec.Code, rec.Header())
		}
		raw := rec.Body.String()
		if !strings.HasPrefix(raw, "\uFEFF") {
			t.Fatalf("el CSV debe comenzar con la marca BOM de UTF-8: %q", raw[:8])
		}
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\uFEFF"))).ReadAll()
		if err != nil || len(rows) != 3 || rows[0][0] != "Fecha local" || rows[1][2] != "'=EVIL()" || rows[2][2] != `Sede, "con" comas` {
			t.Fatalf("csv: %v %v", rows, err)
		}
		if strings.Join(rows[0], ",") != "Fecha local,Enviado el,Sede,Control,Área,Versión de la plantilla,Cumplimiento (%)" {
			t.Fatalf("encabezado: %v", rows[0])
		}
		// 03:00 UTC del 02/01 es 22:00 del 01/01 en Lima: la hora del CSV es local, no UTC.
		if rows[1][1] != "01/01/2026 22:00" || rows[1][6] != "100.00" {
			t.Fatalf("fila: %v", rows[1])
		}
		for _, h := range rows[0] {
			if strings.Contains(strings.ToLower(h), "email") || strings.Contains(strings.ToLower(h), "performed") {
				t.Fatal("el CSV no debe incluir datos de personas")
			}
		}
	})
}

func TestExportPDF(t *testing.T) {
	const base = "/organizations/org/reports/compliance/export.pdf"
	period := "?from=2026-01-01&to=2026-01-31"
	for _, user := range []string{"admin", "chef"} {
		rec := call("GET", base+period, user, "")
		body := rec.Body.Bytes()
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" {
			t.Fatalf("%s: %d %v", user, rec.Code, rec.Header())
		}
		if !bytes.HasPrefix(body, []byte("%PDF-")) || !bytes.HasSuffix(bytes.TrimSpace(body), []byte("%%EOF")) {
			t.Fatalf("%s no parece un PDF: %q", user, body[:16])
		}
		if rec.Header().Get("Content-Disposition") != `attachment; filename="checklists_2026-01-01_2026-01-31.pdf"` ||
			rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
			t.Fatalf("cabeceras: %v", rec.Header())
		}
	}
}

// emptyStore devuelve un período sin controles finalizados.
type emptyStore struct{ fakeStore }

func (emptyStore) CountExportRows(context.Context, domain.Scope, int) (int, error) { return 0, nil }
func (emptyStore) StreamExportRows(context.Context, domain.Scope, func(domain.ExportRow) error) error {
	return nil
}

func TestExportPDFVacio(t *testing.T) {
	work := fakeWork{store: fakeStore{roles: map[string]string{"org|admin": "ADMIN"}}}
	r := chi.NewRouter()
	svc := application.New(emptyWork{emptyStore{work.store}}, fakeVerifier{})
	New(svc, nil).WithPDF(pdf.Render).Register(r, middleware.Authenticate(verifier{}))
	req := httptest.NewRequest("GET", "/organizations/org/reports/compliance/export.pdf?from=2026-01-01&to=2026-01-31", nil)
	req.Header.Set("Authorization", "Bearer admin")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("pdf vacío: %d %q", rec.Code, rec.Body.String())
	}
}

type emptyWork struct{ store emptyStore }

func (w emptyWork) Within(_ context.Context, fn func(application.Store) error) error {
	return fn(w.store)
}

// bigStore simula más filas que el máximo del PDF pero dentro del máximo del CSV.
type bigStore struct{ fakeStore }

func (bigStore) CountExportRows(_ context.Context, _ domain.Scope, limit int) (int, error) {
	return limit + 1, nil
}

type bigWork struct{ store bigStore }

func (w bigWork) Within(_ context.Context, fn func(application.Store) error) error {
	return fn(w.store)
}

func TestExportPDFDemasiadoGrande(t *testing.T) {
	r := chi.NewRouter()
	svc := application.New(bigWork{bigStore{fakeStore{roles: map[string]string{"org|admin": "ADMIN"}}}}, fakeVerifier{})
	New(svc, nil).WithPDF(pdf.Render).Register(r, middleware.Authenticate(verifier{}))
	req := httptest.NewRequest("GET", "/organizations/org/reports/compliance/export.pdf?from=2026-01-01&to=2026-01-31", nil)
	req.Header.Set("Authorization", "Bearer admin")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 422 || code(t, rec) != "EXPORT_TOO_LARGE" || strings.HasPrefix(rec.Header().Get("Content-Type"), "application/pdf") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
