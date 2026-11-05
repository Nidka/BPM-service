package notificationshttp

import (
	"errors"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
	"time"
)

type Handler struct {
	devices *application.Devices
	logger  *slog.Logger
}

func New(d *application.Devices, l *slog.Logger) *Handler { return &Handler{d, l} }
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Post("/organizations/{organizationID}/notifications/devices", h.register)
		r.Delete("/organizations/{organizationID}/notifications/devices/{deviceID}", h.deactivate)
	})
}
func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return "", "", false
	}
	org := chi.URLParam(r, "organizationID")
	if !domain.ValidOrganization(org) {
		h.fail(w, r, domain.ErrNotFound)
		return "", "", false
	}
	return user, org, true
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, e error) {
	id := middleware.RequestIDFromContext(r.Context())
	var invalid *domain.ValidationError
	switch {
	case errors.As(e, &invalid):
		response.ValidationError(w, id, invalid.Fields)
	case errors.Is(e, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene acceso a esta empresa."})
	case errors.Is(e, domain.ErrNotFound):
		response.Error(w, 404, id, response.APIError{Code: "NOT_FOUND", Message: "El registro no está disponible."})
	case errors.Is(e, domain.ErrConflict):
		response.Error(w, 409, id, response.APIError{Code: "RESOURCE_CONFLICT", Message: "No se pudo vincular este dispositivo. Vuelve a iniciar sesión e inténtalo de nuevo."})
	default:
		if h.logger != nil {
			h.logger.Error("notification device request failed", "requestId", id)
		}
		response.InternalError(w, id)
	}
}

type deviceDTO struct {
	ID         string     `json:"id"`
	Platform   string     `json:"platform,omitempty"`
	Status     string     `json:"status"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v struct {
		ID             string `json:"id"`
		InstallationID string `json:"installationId"`
		Platform       string `json:"platform"`
		PushToken      string `json:"pushToken"`
	}
	if !request.DecodeStrictJSON(w, r, &v, 512<<10) {
		return
	}
	out, e := h.devices.Register(r.Context(), user, org, domain.DeviceInput{ID: v.ID, InstallationID: v.InstallationID, Platform: v.Platform, PushToken: v.PushToken})
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": deviceDTO{out.ID, out.Platform, out.Status, out.LastSeenAt}})
}
func (h *Handler) deactivate(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la solicitud."})
		return
	}
	out, e := h.devices.Deactivate(r.Context(), user, org, chi.URLParam(r, "deviceID"))
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": deviceDTO{ID: out.ID, Status: out.Status}})
}
