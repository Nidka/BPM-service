// Package platformsettingshttp expone el contacto de soporte de la plataforma.
package platformsettingshttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/platformsettings/domain"
	"github.com/go-chi/chi/v5"
)

// Service son los casos de uso que necesita el handler.
type Service interface {
	SupportContact(ctx context.Context, user string) (domain.SupportContact, error)
	UpdateSupportContact(ctx context.Context, user, email, whatsapp string) (domain.SupportContact, error)
}

type Handler struct {
	service Service
	logger  *slog.Logger
}

func New(service Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// Register monta GET y PUT /platform/support.
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/platform/support", h.get)
		r.Put("/platform/support", h.put)
	})
}

type supportDTO struct {
	SupportEmail    string  `json:"supportEmail"`
	SupportWhatsapp *string `json:"supportWhatsapp"`
	UpdatedAt       *string `json:"updatedAt,omitempty"`
}

func toDTO(c domain.SupportContact) supportDTO {
	out := supportDTO{SupportEmail: c.Email}
	if c.WhatsApp != "" {
		w := c.WhatsApp
		out.SupportWhatsapp = &w
	}
	if !c.UpdatedAt.IsZero() {
		u := c.UpdatedAt.UTC().Format(time.RFC3339)
		out.UpdatedAt = &u
	}
	return out
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	c, err := h.service.SupportContact(r.Context(), user)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": toDTO(c)})
}

type supportBody struct {
	SupportEmail    string  `json:"supportEmail"`
	SupportWhatsapp *string `json:"supportWhatsapp"`
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	var body supportBody
	if !request.DecodeStrictJSON(w, r, &body, 4096) {
		return
	}
	whatsapp := ""
	if body.SupportWhatsapp != nil {
		whatsapp = *body.SupportWhatsapp
	}
	c, err := h.service.UpdateSupportContact(r.Context(), user, body.SupportEmail, whatsapp)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": toDTO(c)})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene permiso para esta operación."})
	default:
		if h.logger != nil {
			h.logger.Error("platform settings request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
		}
		response.InternalError(w, id)
	}
}
