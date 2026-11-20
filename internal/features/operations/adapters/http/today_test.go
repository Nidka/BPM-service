package operationshttp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/operations/application"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
)

const (
	todayOrg  = "10000000-0000-0000-0000-000000000001"
	todaySite = "20000000-0000-0000-0000-000000000001"
)

// todayStore es un repositorio en memoria de planes y actividad; el resto del Store no se usa.
type todayStore struct {
	application.Store
	role           string
	denySite       bool
	zone           string
	plans          []domain.CleaningPlan
	shifts         []domain.Shift
	activity       map[string]domain.PlanActivity
	activityCalls  int
	activityBatch  []domain.PlanPeriod
	activityUserID string
}

func (s *todayStore) Within(_ context.Context, fn func(application.Store) error) error { return fn(s) }
func (s *todayStore) Actor(_ context.Context, user, _ string) (domain.Actor, error) {
	return domain.Actor{ID: user, Role: s.role}, nil
}
func (s *todayStore) Location(context.Context, domain.Actor, string, string) (string, error) {
	if s.denySite {
		return "", domain.ErrDenied
	}
	return s.zone, nil
}
func (s *todayStore) page(w pagination.Window, activeOnly bool) []domain.CleaningPlan {
	rows := []domain.CleaningPlan{}
	for _, p := range s.plans {
		if (!activeOnly || p.Active) && strings.Contains(strings.ToLower(p.Area+" "+p.Task), strings.ToLower(w.Query)) {
			rows = append(rows, p)
		}
	}
	key := func(p domain.CleaningPlan) string { return strings.ToLower(p.Area + " " + p.Task) }
	sort.Slice(rows, func(i, j int) bool { return key(rows[i]) < key(rows[j]) })
	out := []domain.CleaningPlan{}
	for _, p := range rows {
		if w.After.ID != "" && key(p) <= w.After.Key {
			continue
		}
		out = append(out, p)
	}
	size := w.Size
	if size == 0 {
		size = pagination.DefaultSize
	}
	if len(out) > size+1 {
		out = out[:size+1]
	}
	return out
}
func (s *todayStore) Plans(_ context.Context, _, _ string, w pagination.Window) ([]domain.CleaningPlan, error) {
	return s.page(w, false), nil
}
func (s *todayStore) ActivePlans(_ context.Context, _, _ string, w pagination.Window) ([]domain.CleaningPlan, error) {
	return s.page(w, true), nil
}
func (s *todayStore) ActiveShifts(context.Context, string, string) ([]domain.Shift, error) {
	return s.shifts, nil
}
func (s *todayStore) PlanActivity(_ context.Context, _, _, user string, periods []domain.PlanPeriod) (map[string]domain.PlanActivity, error) {
	s.activityCalls++
	s.activityBatch, s.activityUserID = periods, user
	return s.activity, nil
}

func todayRouter(store *todayStore) http.Handler {
	router := chi.NewRouter()
	New(application.New(store), slog.New(slog.NewTextHandler(io.Discard, nil))).Register(router, middleware.Authenticate(verifier{}))
	return router
}

func getPlans(t *testing.T, router http.Handler, query string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/organizations/"+todayOrg+"/cleaning-plans?"+query, nil)
	req.Header.Set("Authorization", "Bearer token")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res.Code, res.Body.String()
}

func newTodayStore() *todayStore {
	return &todayStore{
		role: "WORKER", zone: "America/Lima",
		plans: []domain.CleaningPlan{
			{ID: "10000000-0000-0000-0000-0000000000a1", LocationID: todaySite, Area: "Cocina", Task: "Limpiar mesa", Instructions: "Con desinfectante", Frequency: "DAILY", DueTime: "00:00", Active: true},
			{ID: "10000000-0000-0000-0000-0000000000a2", LocationID: todaySite, Area: "Cocina", Task: "Limpiar campana", Frequency: "WEEKLY", Active: true},
			{ID: "10000000-0000-0000-0000-0000000000a3", LocationID: todaySite, Area: "Almacén", Task: "Plan retirado", Frequency: "DAILY", Active: false},
			{ID: "10000000-0000-0000-0000-0000000000a4", LocationID: todaySite, Area: "Salón", Task: "Cierre de turno", Frequency: "PER_SHIFT", Active: true},
		},
		activity: map[string]domain.PlanActivity{},
	}
}

type listBody struct {
	Data       []map[string]any `json:"data"`
	Pagination struct {
		NextPageToken string `json:"nextPageToken"`
	} `json:"pagination"`
}

func decodeList(t *testing.T, body string) listBody {
	t.Helper()
	var out listBody
	if e := json.Unmarshal([]byte(body), &out); e != nil {
		t.Fatal(e, body)
	}
	return out
}

func TestPlansWithoutViewKeepTheExactPreviousResponse(t *testing.T) {
	router := todayRouter(newTodayStore())
	want := `{"data":[` +
		`{"id":"10000000-0000-0000-0000-0000000000a3","locationId":"` + todaySite + `","area":"Almacén","task":"Plan retirado","instructions":"","productName":"","frequency":"DAILY","dueTime":"","active":false},` +
		`{"id":"10000000-0000-0000-0000-0000000000a2","locationId":"` + todaySite + `","area":"Cocina","task":"Limpiar campana","instructions":"","productName":"","frequency":"WEEKLY","dueTime":"","active":true},` +
		`{"id":"10000000-0000-0000-0000-0000000000a1","locationId":"` + todaySite + `","area":"Cocina","task":"Limpiar mesa","instructions":"Con desinfectante","productName":"","frequency":"DAILY","dueTime":"00:00","active":true},` +
		`{"id":"10000000-0000-0000-0000-0000000000a4","locationId":"` + todaySite + `","area":"Salón","task":"Cierre de turno","instructions":"","productName":"","frequency":"PER_SHIFT","dueTime":"","active":true}` +
		`],"pagination":{"nextPageToken":""}}`
	code, body := getPlans(t, router, "locationId="+todaySite)
	if code != 200 || strings.TrimSpace(body) != want {
		t.Fatalf("la respuesta sin view cambió:\n%d %s\nwant %s", code, body, want)
	}
	if strings.Contains(body, "today") {
		t.Fatal("sin view no debe haber today")
	}
}

func TestPlansViewParameterValidation(t *testing.T) {
	router := todayRouter(newTodayStore())
	for _, query := range []string{
		"locationId=" + todaySite + "&view=",
		"locationId=" + todaySite + "&view=week",
		"locationId=" + todaySite + "&view=TODAY",
		"locationId=" + todaySite + "&view=today&view=today",
		"locationId=" + todaySite + "&view=today&role=ADMIN",
		"view=today",
		"locationId=" + todaySite + "&view=today&pageSize=0",
	} {
		code, body := getPlans(t, router, query)
		if code != 400 || !strings.Contains(body, "INVALID_PAGINATION") {
			t.Fatal(query, code, body)
		}
	}
	// Las otras rutas de lectura no adoptan el parámetro.
	req := httptest.NewRequest("GET", "/organizations/"+todayOrg+"/cleaning-executions?locationId="+todaySite+"&view=today", nil)
	req.Header.Set("Authorization", "Bearer token")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 400 {
		t.Fatal("cleaning-executions aceptó view", res.Code)
	}
}

func TestTodayViewReturnsOnlyActivePlansWithTodayObject(t *testing.T) {
	store := newTodayStore()
	store.shifts = []domain.Shift{{ID: "10000000-0000-0000-0000-0000000000b1", Name: "Siempre", StartTime: "00:00", EndTime: "12:00", Active: true}, {ID: "10000000-0000-0000-0000-0000000000b2", Name: "Resto", StartTime: "12:00", EndTime: "00:00", Active: true}}
	mine := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	store.activity["10000000-0000-0000-0000-0000000000a2"] = domain.PlanActivity{Done: true, DoneByMe: true, LastExecutionID: "10000000-0000-0000-0000-0000000000e1", LastResult: "OBSERVED", LastExecutedAt: &mine}
	store.activity["10000000-0000-0000-0000-0000000000a1"] = domain.PlanActivity{Done: true} // lo hizo un compañero
	code, body := getPlans(t, todayRouter(store), "locationId="+todaySite+"&view=today")
	if code != 200 {
		t.Fatal(code, body)
	}
	list := decodeList(t, body)
	if len(list.Data) != 3 {
		t.Fatal("solo planes activos", body)
	}
	byTask := map[string]map[string]any{}
	for _, row := range list.Data {
		byTask[row["task"].(string)] = row
		if row["active"] != true {
			t.Fatal("plan inactivo en la vista", row)
		}
	}
	teammate := byTask["Limpiar mesa"]["today"].(map[string]any)
	if teammate["done"] != true || teammate["doneByMe"] != false || teammate["lastExecutionId"] != nil || teammate["lastResult"] != nil || teammate["lastExecutedAt"] != nil || teammate["shift"] != nil || teammate["timezone"] != "America/Lima" {
		t.Fatal("compañero", teammate)
	}
	// dueTime 00:00 solo vence si ya pasó la medianoche local; con done=true nunca vence.
	if teammate["overdue"] != false {
		t.Fatal("hecho por otra persona no vence", teammate)
	}
	own := byTask["Limpiar campana"]["today"].(map[string]any)
	if own["done"] != true || own["doneByMe"] != true || own["lastExecutionId"] != "10000000-0000-0000-0000-0000000000e1" || own["lastResult"] != "OBSERVED" || own["lastExecutedAt"] != "2026-11-20T20:00:00Z" || own["overdue"] != false {
		t.Fatal("propia", own)
	}
	for _, key := range []string{"periodStartsAt", "periodEndsAt"} {
		if _, e := time.Parse(time.RFC3339, own[key].(string)); e != nil {
			t.Fatal(key, own[key])
		}
	}
	perShift := byTask["Cierre de turno"]["today"].(map[string]any)
	shift, ok := perShift["shift"].(map[string]any)
	if !ok || shift["state"] != "OPEN" || (shift["name"] != "Siempre" && shift["name"] != "Resto") || shift["id"] == "" || perShift["done"] != false {
		t.Fatal("turno abierto", perShift)
	}
	// Ninguna respuesta expone responsables ni nombres.
	if strings.Contains(body, "responsible") {
		t.Fatal("expuso un responsable", body)
	}
	// Una sola consulta de actividad para toda la página, con el usuario autenticado.
	if store.activityCalls != 1 || len(store.activityBatch) != 3 || store.activityUserID != "chef" {
		t.Fatal("N+1 o usuario incorrecto", store.activityCalls, len(store.activityBatch), store.activityUserID)
	}
	for _, p := range store.activityBatch {
		if p.PlanID == "10000000-0000-0000-0000-0000000000a4" && p.ShiftID == "" {
			t.Fatal("el plan por turno debe buscar con su turno")
		}
		if !p.EndsAt.After(p.StartsAt) {
			t.Fatal("período vacío", p)
		}
	}
}

func TestTodayViewPerShiftWithoutShiftsIsNotDone(t *testing.T) {
	store := newTodayStore()
	store.activity["10000000-0000-0000-0000-0000000000a4"] = domain.PlanActivity{Done: true, DoneByMe: true} // no debe leerse
	code, body := getPlans(t, todayRouter(store), "locationId="+todaySite+"&view=today&query=cierre")
	list := decodeList(t, body)
	if code != 200 || len(list.Data) != 1 {
		t.Fatal(code, body)
	}
	today := list.Data[0]["today"].(map[string]any)
	if today["shift"] != nil || today["done"] != false || today["doneByMe"] != false || today["overdue"] != false {
		t.Fatal(today)
	}
	if store.activityCalls != 0 {
		t.Fatal("no hay ocurrencia que consultar")
	}
}

func TestTodayViewRespectsLocationAccess(t *testing.T) {
	store := newTodayStore()
	store.denySite = true
	if code, body := getPlans(t, todayRouter(store), "locationId="+todaySite+"&view=today"); code != 403 || !strings.Contains(body, "ACCESS_DENIED") {
		t.Fatal("sede no asignada", code, body)
	}
}

func TestCursorIsBoundToTheView(t *testing.T) {
	store := newTodayStore()
	router := todayRouter(store)
	base := "locationId=" + todaySite + "&pageSize=1"
	code, body := getPlans(t, router, base+"&view=today")
	today := decodeList(t, body)
	if code != 200 || len(today.Data) != 1 || today.Pagination.NextPageToken == "" {
		t.Fatal(code, body)
	}
	code, body = getPlans(t, router, base)
	plain := decodeList(t, body)
	if code != 200 || plain.Pagination.NextPageToken == "" {
		t.Fatal(code, body)
	}
	// Cada cursor solo vale en su propia vista.
	if code, body = getPlans(t, router, base+"&pageToken="+today.Pagination.NextPageToken); code != 400 || !strings.Contains(body, "INVALID_PAGINATION") {
		t.Fatal("cursor de today aceptado sin view", code, body)
	}
	if code, body = getPlans(t, router, base+"&view=today&pageToken="+plain.Pagination.NextPageToken); code != 400 || !strings.Contains(body, "INVALID_PAGINATION") {
		t.Fatal("cursor sin view aceptado en today", code, body)
	}
	// Y continúa correctamente dentro de la misma vista.
	code, body = getPlans(t, router, base+"&view=today&pageToken="+today.Pagination.NextPageToken)
	next := decodeList(t, body)
	if code != 200 || len(next.Data) != 1 || next.Data[0]["id"] == today.Data[0]["id"] || next.Data[0]["today"] == nil {
		t.Fatal("segunda página", code, body)
	}
}
