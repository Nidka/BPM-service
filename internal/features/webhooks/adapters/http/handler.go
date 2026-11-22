// Package webhookshttp expone la gestión de webhooks de una empresa (RF-48). El secreto solo se devuelve al
// crear o rotar; nunca se registra en logs.
package webhookshttp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/webhooks/application"
	"github.com/Nidka/BPM-service/internal/features/webhooks/domain"
	"github.com/go-chi/chi/v5"
)

const maxBody = 8 << 10

type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// Register monta las rutas bajo /organizations/{organizationID}/webhooks.
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/organizations/{organizationID}/webhooks", h.list)
		r.Post("/organizations/{organizationID}/webhooks", h.create)
		r.Put("/organizations/{organizationID}/webhooks/{webhookID}", h.update)
		r.Delete("/organizations/{organizationID}/webhooks/{webhookID}", h.delete)
		r.Post("/organizations/{organizationID}/webhooks/{webhookID}/rotate-secret", h.rotate)
		r.Post("/organizations/{organizationID}/webhooks/{webhookID}/test", h.test)
	})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Solo el administrador de la empresa gestiona sus webhooks."})
	case errors.Is(err, domain.ErrNotFound):
		response.Error(w, 404, id, response.APIError{Code: "WEBHOOK_NOT_FOUND", Message: "El webhook ya no existe."})
	case errors.Is(err, domain.ErrLimit):
		response.Error(w, 409, id, response.APIError{Code: "WEBHOOK_LIMIT", Message: fmt.Sprintf("Puedes registrar hasta %d webhooks.", domain.MaxEndpoints)})
	default:
		if h.logger != nil {
			h.logger.Error("webhook request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
		}
		response.InternalError(w, id)
	}
}

type endpointDTO struct {
	ID             string   `json:"id"`
	URL            string   `json:"url"`
	EventTypes     []string `json:"eventTypes"`
	Active         bool     `json:"active"`
	LastStatus     *int     `json:"lastStatus"`
	LastError      string   `json:"lastError,omitempty"`
	LastDeliveryAt *string  `json:"lastDeliveryAt"`
	// Secret solo aparece al crear o rotar.
	Secret string `json:"secret,omitempty"`
}

func toDTO(e domain.Endpoint, withSecret bool) endpointDTO {
	out := endpointDTO{ID: e.ID, URL: e.URL, EventTypes: e.EventTypes, Active: e.Active, LastStatus: e.LastStatus, LastError: e.LastError}
	if e.LastDeliveryAt != nil {
		at := e.LastDeliveryAt.UTC().Format(time.RFC3339)
		out.LastDeliveryAt = &at
	}
	if withSecret {
		out.Secret = e.Secret
	}
	return out
}

type body struct {
	URL        string   `json:"url"`
	EventTypes []string `json:"eventTypes"`
	Active     *bool    `json:"active"`
}

func (b body) input() domain.Input {
	active := true
	if b.Active != nil {
		active = *b.Active
	}
	return domain.Input{URL: b.URL, EventTypes: b.EventTypes, Active: active}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	items, err := h.service.List(r.Context(), user, chi.URLParam(r, "organizationID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]endpointDTO, 0, len(items))
	for _, e := range items {
		out = append(out, toDTO(e, false))
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"items": out, "eventTypes": domain.EventTypes, "maxEndpoints": domain.MaxEndpoints}})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	var in body
	if !request.DecodeStrictJSON(w, r, &in, maxBody) {
		return
	}
	e, err := h.service.Create(r.Context(), user, chi.URLParam(r, "organizationID"), in.input())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 201, map[string]any{"data": toDTO(e, true)})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	var in body
	if !request.DecodeStrictJSON(w, r, &in, maxBody) {
		return
	}
	e, err := h.service.Update(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "webhookID"), in.input())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": toDTO(e, false)})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	if err := h.service.Delete(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "webhookID")); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) rotate(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	e, err := h.service.RotateSecret(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "webhookID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": toDTO(e, true)})
}

// test devuelve 200 con el resultado aunque el destino falle: el fallo es del destino, no de la solicitud.
func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	status, err := h.service.Test(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "webhookID"))
	if errors.Is(err, domain.ErrDenied) || errors.Is(err, domain.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"delivered": err == nil, "status": status}})
}
