// Package usagehttp expone el registro de uso por HTTP. No guarda ni registra cuerpos.
package usagehttp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/usage/application"
	"github.com/Nidka/BPM-service/internal/features/usage/domain"
	"github.com/go-chi/chi/v5"
)

// maxBody limita el lote (50 eventos caben holgados).
const maxBody = 32 << 10

// Handler traduce HTTP a los casos de uso.
type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

// New crea el handler.
func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// Register monta las rutas bajo /organizations/{organizationID}/usage.
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Post("/organizations/{organizationID}/usage/events", h.record)
		r.Get("/organizations/{organizationID}/usage/summary", h.summary)
	})
}

type eventRequest struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"sessionId"`
	Type            string    `json:"type"`
	Module          string    `json:"module"`
	OccurredAt      time.Time `json:"occurredAt"`
	DurationSeconds *int      `json:"durationSeconds"`
}

type batchRequest struct {
	Events []eventRequest `json:"events"`
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene permiso para esta operación."})
	case errors.Is(err, domain.ErrSessionConflict):
		response.Error(w, 409, id, response.APIError{Code: "USAGE_SESSION_CONFLICT", Message: "La sesión ya existe con otro propietario."})
	case errors.Is(err, domain.ErrIdempotencyConflict):
		response.Error(w, 409, id, response.APIError{Code: "IDEMPOTENCY_CONFLICT", Message: "Un evento ya se usó con otro contenido."})
	default:
		if h.logger != nil {
			h.logger.Error("usage request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
		}
		response.InternalError(w, id)
	}
}

func (h *Handler) record(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	var in batchRequest
	if !request.DecodeStrictJSON(w, r, &in, maxBody) {
		return
	}
	batch := make([]domain.Event, 0, len(in.Events))
	for _, e := range in.Events {
		batch = append(batch, domain.Event{ID: e.ID, SessionID: e.SessionID, Type: e.Type, Module: e.Module, OccurredAt: e.OccurredAt, DurationSeconds: e.DurationSeconds})
	}
	out, err := h.service.Record(r.Context(), user, chi.URLParam(r, "organizationID"), batch)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": map[string]int{"accepted": out.Accepted, "duplicates": out.Duplicates}})
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 2 || len(q["from"]) > 1 || len(q["to"]) > 1 {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura y el tamaño de la solicitud."})
		return
	}
	for k := range q {
		if k != "from" && k != "to" {
			response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura y el tamaño de la solicitud."})
			return
		}
	}
	s, err := h.service.Summary(r.Context(), user, chi.URLParam(r, "organizationID"), q.Get("from"), q.Get("to"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	type module struct {
		Module       string `json:"module"`
		Visits       int    `json:"visits"`
		UniqueUsers  int    `json:"uniqueUsers"`
		TotalSeconds int    `json:"totalSeconds"`
	}
	type day struct {
		Date     string `json:"date"`
		Sessions int    `json:"sessions"`
	}
	mods := make([]module, 0, len(s.Modules))
	for _, m := range s.Modules {
		mods = append(mods, module(m))
	}
	days := make([]day, 0, len(s.Days))
	for _, d := range s.Days {
		days = append(days, day(d))
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{
		"from": s.From, "to": s.To, "sessions": s.Sessions, "endedSessions": s.EndedSessions, "activeUsers": s.ActiveUsers,
		"averageSessionSeconds": s.AverageSessionSeconds, "alertsViewed": s.AlertsViewed, "alertsAttended": s.AlertsAttended,
		"modules": mods, "days": days}})
}
