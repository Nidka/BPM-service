// Package alertshttp traduce el contrato HTTP de reglas y bandeja de avisos.
package alertshttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/alerts/application"
	"github.com/Nidka/BPM-service/internal/features/alerts/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

func New(s *application.Service, l *slog.Logger) *Handler { return &Handler{s, l} }
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		prefix := "/organizations/{organizationID}"
		r.Get(prefix+"/alert-rules", h.rules)
		r.Post(prefix+"/alert-rules", h.create)
		r.Put(prefix+"/alert-rules/{ruleID}", h.update)
		r.Get(prefix+"/alerts", h.inbox)
		r.Get(prefix+"/alerts/unread-count", h.count)
		r.Get(prefix+"/alerts/{alertID}", h.detail)
		r.Post(prefix+"/alerts/{alertID}/view", func(w http.ResponseWriter, r *http.Request) { h.transition(w, r, false) })
		r.Post(prefix+"/alerts/{alertID}/attend", func(w http.ResponseWriter, r *http.Request) { h.transition(w, r, true) })
	})
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, e error) {
	id := middleware.RequestIDFromContext(r.Context())
	switch {
	case errors.Is(e, domain.ErrInvalid):
		response.ValidationError(w, id, map[string]string{"input": "Revisa los valores ingresados."})
	case errors.Is(e, pagination.ErrInvalid):
		response.Error(w, 400, id, response.APIError{Code: "INVALID_PAGINATION", Message: "Revisa la página y los filtros solicitados."})
	case errors.Is(e, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene permiso para esta operación."})
	case errors.Is(e, domain.ErrNotFound):
		response.Error(w, 404, id, response.APIError{Code: "NOT_FOUND", Message: "El aviso o la regla no están disponibles."})
	case errors.Is(e, domain.ErrConflict):
		response.Error(w, 409, id, response.APIError{Code: "RESOURCE_CONFLICT", Message: "El registro cambió o la operación ya tiene otros datos."})
	default:
		if h.logger != nil {
			h.logger.Error("alert request failed", "requestId", id, "errorType", fmt.Sprintf("%T", e))
		}
		response.InternalError(w, id)
	}
}
func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (u, o string, ok bool) {
	u, ok = request.VerifiedUID(w, r)
	if !ok {
		return
	}
	o = chi.URLParam(r, "organizationID")
	if !domain.ValidOrganization(o) || !domain.ValidUID(u) {
		h.fail(w, r, domain.ErrNotFound)
		return "", "", false
	}
	return
}
func invalid(w http.ResponseWriter, r *http.Request) {
	response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura de la solicitud."})
}
func noBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body != nil {
		b, e := io.ReadAll(io.LimitReader(r.Body, 1))
		if e != nil || len(b) > 0 {
			invalid(w, r)
			return false
		}
	}
	return true
}
func query(w http.ResponseWriter, r *http.Request, rule bool) (filter string, p pagination.Request, ok bool) {
	if !noBody(w, r) {
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		invalid(w, r)
		return
	}
	key := "state"
	if rule {
		key = "locationId"
	}
	if values, present := q[key]; present {
		if len(values) != 1 || values[0] == "" {
			invalid(w, r)
			return
		}
		filter = values[0]
		q.Del(key)
	}
	p, e = request.Pagination(q.Encode())
	if e != nil {
		invalid(w, r)
		return
	}
	return filter, p, true
}

type ruleInput struct {
	ID            *string         `json:"id"`
	LocationID    *string         `json:"locationId"`
	Name          *string         `json:"name"`
	Type          *string         `json:"type"`
	Configuration json.RawMessage `json:"configuration"`
	Active        *bool           `json:"active"`
	EmailEnabled  *bool           `json:"emailEnabled"`
	PushEnabled   *bool           `json:"pushEnabled"`
	RowVersion    *int            `json:"rowVersion"`
}

func (v ruleInput) valid(create bool) bool {
	return v.Name != nil && v.Type != nil && len(v.Configuration) > 0 && v.Active != nil && v.EmailEnabled != nil && v.PushEnabled != nil && ((create && v.ID != nil && v.RowVersion == nil) || (!create && v.ID == nil && v.RowVersion != nil))
}
func (v ruleInput) entity(id string) domain.Rule {
	r := domain.Rule{ID: id, LocationID: v.LocationID, Name: *v.Name, Type: *v.Type, Configuration: v.Configuration, Active: *v.Active, EmailEnabled: *v.EmailEnabled, PushEnabled: *v.PushEnabled}
	if v.RowVersion != nil {
		r.RowVersion = *v.RowVersion
	}
	return r
}
func ruleDTO(v domain.Rule) map[string]any {
	return map[string]any{"id": v.ID, "locationId": v.LocationID, "locationName": v.LocationName, "name": v.Name, "type": v.Type, "configuration": v.Configuration, "active": v.Active, "emailEnabled": v.EmailEnabled, "pushEnabled": v.PushEnabled, "rowVersion": v.RowVersion}
}
func alertDTO(v domain.Alert) map[string]any {
	return map[string]any{"id": v.ID, "type": v.Type, "title": v.Title, "message": v.Message, "status": v.Status, "createdAt": v.CreatedAt, "viewedAt": v.ViewedAt, "attendedAt": v.AttendedAt, "locationId": v.LocationID, "locationName": v.LocationName, "timezone": v.Timezone, "sourceType": v.SourceType, "sourceId": v.SourceID, "sourceParentId": v.SourceParentID, "sourceAvailable": v.SourceAvailable}
}
func (h *Handler) rules(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	l, p, ok := query(w, r, true)
	if !ok {
		return
	}
	page, e := h.service.Rules(r.Context(), u, o, l, p)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	items := []any{}
	for _, v := range page.Items {
		items = append(items, ruleDTO(v))
	}
	response.JSON(w, 200, map[string]any{"data": items, "pagination": map[string]string{"nextPageToken": page.NextToken}})
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) { h.write(w, r, true) }
func (h *Handler) update(w http.ResponseWriter, r *http.Request) { h.write(w, r, false) }
func (h *Handler) write(w http.ResponseWriter, r *http.Request, create bool) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input ruleInput
	if !request.DecodeStrictJSON(w, r, &input, 512<<10) {
		return
	}
	if !input.valid(create) {
		invalid(w, r)
		return
	}
	id := chi.URLParam(r, "ruleID")
	if create {
		id = *input.ID
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		invalid(w, r)
		return
	}
	v, e := h.service.WriteRule(r.Context(), u, o, r.Header.Get("Idempotency-Key"), input.entity(id), create)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	status := 200
	if create {
		status = 201
	}
	response.JSON(w, status, map[string]any{"data": ruleDTO(v)})
}
func (h *Handler) inbox(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	state, p, ok := query(w, r, false)
	if !ok {
		return
	}
	page, e := h.service.Inbox(r.Context(), u, o, state, p)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	items := []any{}
	for _, v := range page.Items {
		items = append(items, alertDTO(v))
	}
	response.JSON(w, 200, map[string]any{"data": items, "pagination": map[string]string{"nextPageToken": page.NextToken}})
}
func (h *Handler) count(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		invalid(w, r)
		return
	}
	if !noBody(w, r) {
		return
	}
	n, e := h.service.Count(r.Context(), u, o)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]int{"count": n})
}
func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		invalid(w, r)
		return
	}
	if !noBody(w, r) {
		return
	}
	v, e := h.service.Detail(r.Context(), u, o, chi.URLParam(r, "alertID"))
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": alertDTO(v)})
}
func (h *Handler) transition(w http.ResponseWriter, r *http.Request, attend bool) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	var body struct{}
	if !request.DecodeStrictJSON(w, r, &body, 512<<10) {
		return
	}
	v, e := h.service.Transition(r.Context(), u, o, chi.URLParam(r, "alertID"), attend)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": alertDTO(v)})
}
