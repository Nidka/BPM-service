package incidentshttp

import (
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/incidents/application"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
)

type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

func New(s *application.Service, l *slog.Logger) *Handler { return &Handler{s, l} }
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/organizations/{organizationID}/incident-catalog", h.catalogs)
		r.Post("/organizations/{organizationID}/incident-catalog", h.createCatalog)
		r.Post("/organizations/{organizationID}/incident-catalog/{catalogID}/versions", h.versionCatalog)
		r.Get("/organizations/{organizationID}/incidents", h.incidents)
		r.Post("/organizations/{organizationID}/incidents", h.report)
		r.Get("/organizations/{organizationID}/incidents/{incidentID}", h.detail)
		r.Get("/organizations/{organizationID}/incidents/{incidentID}/assignees", h.assignees)
		r.Get("/organizations/{organizationID}/incidents/{incidentID}/history", h.history)
		r.Post("/organizations/{organizationID}/incidents/{incidentID}/actions", h.act)
	})
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, e error) {
	id := middleware.RequestIDFromContext(r.Context())
	var invalid *domain.ValidationError
	switch {
	case errors.As(e, &invalid):
		response.ValidationError(w, id, invalid.Fields)
	case errors.Is(e, pagination.ErrInvalid):
		response.Error(w, 400, id, response.APIError{Code: "INVALID_PAGINATION", Message: "Revisa la búsqueda, la sede y la página solicitadas."})
	case errors.Is(e, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene permiso para esta operación o sede."})
	case errors.Is(e, domain.ErrNotFound):
		response.Error(w, 404, id, response.APIError{Code: "NOT_FOUND", Message: "El registro no está disponible en esta empresa o sede."})
	case errors.Is(e, domain.ErrConflict):
		response.Error(w, 409, id, response.APIError{Code: "RESOURCE_CONFLICT", Message: "El registro cambió o esta operación ya tiene otros datos. Actualiza el detalle."})
	default:
		if h.logger != nil {
			h.logger.Error("incident request failed", "requestId", id, "errorType", fmt.Sprintf("%T", e))
		}
		response.InternalError(w, id)
	}
}
func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	u, ok := request.VerifiedUID(w, r)
	if !ok {
		return "", "", false
	}
	org := chi.URLParam(r, "organizationID")
	if !domain.ValidOrganization(org) {
		h.fail(w, r, domain.ErrNotFound)
		return "", "", false
	}
	return u, org, true
}
func result[T any](h *Handler, w http.ResponseWriter, r *http.Request, v T, e error) {
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": v})
}
func page[T, U any](h *Handler, w http.ResponseWriter, r *http.Request, p pagination.Page[T], e error, project func(T) U) {
	if e != nil {
		h.fail(w, r, e)
		return
	}
	rows := make([]U, 0, len(p.Items))
	for _, v := range p.Items {
		rows = append(rows, project(v))
	}
	response.JSON(w, 200, map[string]any{"data": rows, "pagination": map[string]string{"nextPageToken": p.NextToken}})
}
