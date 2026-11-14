package operationshttp

import (
	"net/http"
	"net/url"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	request "github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
)

// registerPlus añade turnos por sede y movimientos de stock al mismo grupo autenticado.
func (h *Handler) registerPlus(r chi.Router) {
	r.Get("/organizations/{organizationID}/shifts", h.shifts)
	r.Post("/organizations/{organizationID}/shifts", h.createShift)
	r.Get("/organizations/{organizationID}/shifts/{shiftID}", h.shift)
	r.Put("/organizations/{organizationID}/shifts/{shiftID}", h.updateShift)
	r.Post("/organizations/{organizationID}/shifts/{shiftID}/deactivate", h.deactivateShift)
	r.Get("/organizations/{organizationID}/stock-movements", h.movements)
	r.Post("/organizations/{organizationID}/stock-movements", h.move)
	r.Get("/organizations/{organizationID}/stock-movements/{movementID}", h.movement)
}

type shiftInput struct {
	ID         string `json:"id"`
	LocationID string `json:"locationId"`
	Name       string `json:"name"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
}
type shiftUpdateInput struct {
	Name       string `json:"name"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	RowVersion int    `json:"rowVersion"`
}
type shiftDTO struct {
	shiftInput
	Timezone   string    `json:"timezone"`
	Crosses    bool      `json:"crossesMidnight"`
	Active     bool      `json:"active"`
	OpenNow    bool      `json:"openNow"`
	RowVersion int       `json:"rowVersion"`
	CreatedAt  time.Time `json:"createdAt"`
}

func shift(v domain.Shift) shiftDTO {
	crosses := false
	if w, e := v.Window(); e == nil {
		crosses = w.Crosses()
	}
	return shiftDTO{shiftInput{v.ID, v.LocationID, v.Name, v.StartTime, v.EndTime}, v.Timezone, crosses, v.Active, v.OpenNow, v.RowVersion, v.CreatedAt}
}

type movementInput struct {
	ID                string    `json:"id"`
	LocationID        string    `json:"locationId"`
	LotID             string    `json:"lotId"`
	Type              string    `json:"type"`
	Quantity          string    `json:"quantity"`
	Reason            string    `json:"reason,omitempty"`
	Note              string    `json:"note,omitempty"`
	ClientOperationID string    `json:"clientOperationId"`
	ClientCreatedAt   time.Time `json:"clientCreatedAt"`
}

func (v movementInput) domain() domain.StockMovement {
	return domain.StockMovement{ID: v.ID, LocationID: v.LocationID, LotID: v.LotID, Type: v.Type, Quantity: v.Quantity, Reason: v.Reason, Note: v.Note, ClientOperationID: v.ClientOperationID, ClientCreatedAt: v.ClientCreatedAt}
}

type movementDTO struct {
	movementInput
	ProductName     string    `json:"productName"`
	Unit            string    `json:"unit"`
	BatchCode       string    `json:"batchCode"`
	BalanceAfter    string    `json:"balanceAfter"`
	PerformedBy     string    `json:"performedBy"`
	PerformedByName string    `json:"performedByName"`
	CreatedAt       time.Time `json:"createdAt"`
}

func movement(v domain.StockMovement) movementDTO {
	return movementDTO{movementInput{v.ID, v.LocationID, v.LotID, v.Type, canonicalQuantity(v.Quantity), v.Reason, v.Note, v.ClientOperationID, v.ClientCreatedAt}, v.ProductName, v.Unit, v.BatchCode, canonicalQuantity(v.BalanceAfter), v.PerformedBy, v.PerformedByName, v.CreatedAt}
}

// filteredQuery exige locationId y acepta únicamente los filtros nombrados, además de la paginación.
func (h *Handler) filteredQuery(w http.ResponseWriter, r *http.Request, filters ...string) (string, map[string]string, pagination.Request, bool) {
	var p pagination.Request
	q, e := url.ParseQuery(r.URL.RawQuery)
	site := ""
	if e == nil {
		values := q["locationId"]
		if len(values) != 1 || !domain.ValidID(values[0]) {
			e = pagination.ErrInvalid
		} else {
			site = values[0]
		}
		q.Del("locationId")
	}
	extra := map[string]string{}
	for _, name := range filters {
		if values, ok := q[name]; ok {
			if len(values) != 1 {
				e = pagination.ErrInvalid
			} else {
				extra[name] = values[0]
			}
			q.Del(name)
		}
	}
	if e == nil {
		p, e = request.Pagination(q.Encode())
	}
	if e != nil {
		h.fail(w, r, pagination.ErrInvalid)
		return "", nil, p, false
	}
	return site, extra, p, true
}

func (h *Handler) shifts(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	site, extra, q, ok := h.filteredQuery(w, r, "status")
	if !ok {
		return
	}
	all := false
	switch extra["status"] {
	case "", "ACTIVE":
	case "ALL":
		all = true
	default:
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	v, e := h.service.Shifts(r.Context(), u, o, site, all, q)
	page(h, w, r, v, e, shift)
}
func (h *Handler) shift(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	v, e := h.service.Shift(r.Context(), u, o, chi.URLParam(r, "shiftID"))
	result(h, w, r, shift(v), e)
}
func (h *Handler) createShift(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[shiftInput](w, r)
	if !ok {
		return
	}
	v, e := h.service.CreateShift(r.Context(), u, o, r.Header.Get("Idempotency-Key"), domain.Shift{ID: input.ID, LocationID: input.LocationID, Name: input.Name, StartTime: input.StartTime, EndTime: input.EndTime})
	result(h, w, r, shift(v), e)
}
func (h *Handler) updateShift(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[shiftUpdateInput](w, r)
	if !ok {
		return
	}
	v, e := h.service.UpdateShift(r.Context(), u, o, chi.URLParam(r, "shiftID"), r.Header.Get("Idempotency-Key"), domain.Shift{Name: input.Name, StartTime: input.StartTime, EndTime: input.EndTime, RowVersion: input.RowVersion})
	result(h, w, r, shift(v), e)
}
func (h *Handler) deactivateShift(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[struct {
		RowVersion int `json:"rowVersion"`
	}](w, r)
	if !ok {
		return
	}
	v, e := h.service.DeactivateShift(r.Context(), u, o, chi.URLParam(r, "shiftID"), r.Header.Get("Idempotency-Key"), input.RowVersion)
	result(h, w, r, shift(v), e)
}
func (h *Handler) move(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[movementInput](w, r)
	if !ok {
		return
	}
	v, e := h.service.Move(r.Context(), u, o, r.Header.Get("Idempotency-Key"), input.domain())
	result(h, w, r, movement(v), e)
}
func (h *Handler) movements(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	site, extra, q, ok := h.filteredQuery(w, r, "lotId", "type")
	if !ok {
		return
	}
	v, e := h.service.Movements(r.Context(), u, o, site, domain.MovementFilter{LotID: extra["lotId"], Type: extra["type"]}, q)
	page(h, w, r, v, e, movement)
}
func (h *Handler) movement(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_PAGINATION", Message: "Revisa la búsqueda, la sede y la página solicitadas."})
		return
	}
	v, e := h.service.Movement(r.Context(), u, o, chi.URLParam(r, "movementID"))
	result(h, w, r, movement(v), e)
}
