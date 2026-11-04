package mediahttp

import (
	"errors"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	request "github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/media/application"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"github.com/go-chi/chi/v5"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service, logger}
}

type fileDTO struct {
	ID           string     `json:"id"`
	IncidentID   string     `json:"incidentId,omitempty"`
	ActionID     string     `json:"actionId,omitempty"`
	Stage        string     `json:"stage,omitempty"`
	RunID        string     `json:"runId"`
	ItemID       string     `json:"itemId"`
	OriginalName string     `json:"originalName"`
	MIMEType     string     `json:"mimeType"`
	SizeBytes    int64      `json:"sizeBytes"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"createdAt"`
	UploadedAt   *time.Time `json:"uploadedAt,omitempty"`
}

func file(f domain.File) fileDTO {
	return fileDTO{ID: f.ID, RunID: f.RunID, ItemID: f.ItemID, IncidentID: f.IncidentID, ActionID: f.ActionID, Stage: f.Stage, OriginalName: f.OriginalName, MIMEType: f.MIMEType, SizeBytes: f.SizeBytes, Status: f.Status, CreatedAt: f.CreatedAt, UploadedAt: f.UploadedAt}
}

type ticketDTO struct {
	URL       string              `json:"url"`
	Headers   map[string][]string `json:"headers"`
	ExpiresAt time.Time           `json:"expiresAt"`
}

func ticket(v contracts.SignedRequest) ticketDTO {
	headers := map[string][]string{}
	// Host y Content-Length los calcula cada motor HTTP, también en navegador.
	for key, values := range v.Headers {
		switch strings.ToLower(key) {
		case "content-type", "if-none-match", "x-amz-meta-sha256", "cache-control", "content-disposition":
			headers[key] = values
		}
	}
	return ticketDTO{v.URL, headers, v.ExpiresAt}
}
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/organizations/{organizationID}/checklist-runs/{runID}/items/{itemID}/evidence", h.list)
		r.Get("/organizations/{organizationID}/incidents/{incidentID}/evidence/BEFORE", h.listIncident)
		r.Get("/organizations/{organizationID}/incidents/{incidentID}/actions/{actionID}/evidence/AFTER", h.listIncident)
		r.Post("/organizations/{organizationID}/media/uploads", h.reserve)
		r.Post("/organizations/{organizationID}/media/{mediaID}/confirm", h.confirm)
		r.Post("/organizations/{organizationID}/media/{mediaID}/download", h.download)
		r.Delete("/organizations/{organizationID}/media/{mediaID}", h.remove)
	})
}
func (h *Handler) uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.URL.RawQuery != "" {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Esta consulta no admite parámetros adicionales."})
		return "", false
	}
	return request.VerifiedUID(w, r)
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "No tienes permiso para esta foto o revisión."})
	case errors.Is(err, domain.ErrNotFound):
		response.Error(w, 404, id, response.APIError{Code: "NOT_FOUND", Message: "La evidencia o la pregunta no están disponibles."})
	case errors.Is(err, domain.ErrConflict):
		response.Error(w, 409, id, response.APIError{Code: "RESOURCE_CONFLICT", Message: "La evidencia cambió o no puede modificarse. Consulta su estado antes de continuar."})
	case errors.Is(err, domain.ErrNotReady):
		response.Error(w, 409, id, response.APIError{Code: "UPLOAD_NOT_READY", Message: "Todavía no se ha confirmado la carga de esta foto."})
	case errors.Is(err, domain.ErrInvalidFile):
		response.Error(w, 422, id, response.APIError{Code: "MEDIA_CONTENT_INVALID", Message: "La foto recibida no coincide o no tiene un formato válido. Retírala y selecciona otra vez el archivo."})
	case errors.Is(err, domain.ErrLimit):
		response.Error(w, 422, id, response.APIError{Code: "MEDIA_LIMIT_REACHED", Message: "Alcanzaste el límite de fotos de esta pregunta. Retira una antes de agregar otra."})
	case errors.Is(err, domain.ErrUnavailable):
		response.Error(w, 503, id, response.APIError{Code: "MEDIA_UNAVAILABLE", Message: "No pudimos acceder a las fotos. Inténtalo de nuevo."})
	default:
		h.logger.Error("media request failed", "request_id", id, "error_type", "internal")
		response.InternalError(w, id)
	}
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	user, ok := h.uid(w, r)
	if !ok {
		return
	}
	data, err := h.service.List(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "runID"), chi.URLParam(r, "itemID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	respondCollection(w, data)
}
func (h *Handler) reserve(w http.ResponseWriter, r *http.Request) {
	user, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in struct {
		ID           string `json:"id"`
		RunID        string `json:"runId"`
		IncidentID   string `json:"incidentId"`
		ActionID     string `json:"actionId"`
		Stage        string `json:"stage"`
		ItemID       string `json:"itemId"`
		OriginalName string `json:"originalName"`
		MIMEType     string `json:"mimeType"`
		SizeBytes    int64  `json:"sizeBytes"`
		SHA256       string `json:"sha256"`
	}
	if !request.DecodeJSON(w, r, &in, 4096) {
		return
	}
	value, err := h.service.Reserve(r.Context(), user, chi.URLParam(r, "organizationID"), domain.UploadInput{ID: in.ID, RunID: in.RunID, ItemID: in.ItemID, IncidentID: in.IncidentID, ActionID: in.ActionID, Stage: in.Stage, OriginalName: in.OriginalName, MIMEType: in.MIMEType, SizeBytes: in.SizeBytes, SHA256: in.SHA256})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var upload *ticketDTO
	if value.Upload != nil {
		v := ticket(*value.Upload)
		upload = &v
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"file": file(value.File), "upload": upload}})
}
func emptyJSON(w http.ResponseWriter, r *http.Request) bool {
	var input struct{}
	return request.DecodeJSON(w, r, &input, 1024)
}
func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	user, ok := h.uid(w, r)
	if !ok || !emptyJSON(w, r) {
		return
	}
	f, err := h.service.Confirm(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "mediaID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": file(f)})
}
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	user, ok := h.uid(w, r)
	if !ok || !emptyJSON(w, r) {
		return
	}
	f, err := h.service.Download(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "mediaID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": ticket(f)})
}
func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	user, ok := h.uid(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) > 0 {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Esta operación no admite contenido adicional."})
		return
	}
	f, err := h.service.Remove(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "mediaID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": file(f)})
}

func (h *Handler) listIncident(w http.ResponseWriter, r *http.Request) {
	user, ok := h.uid(w, r)
	if !ok {
		return
	}
	action := chi.URLParam(r, "actionID")
	stage := "BEFORE"
	if action != "" {
		stage = "AFTER"
	}
	data, err := h.service.ListIncident(r.Context(), user, chi.URLParam(r, "organizationID"), chi.URLParam(r, "incidentID"), action, stage)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	respondCollection(w, data)
}

func respondCollection(w http.ResponseWriter, data application.Collection) {
	files := make([]fileDTO, 0, len(data.Files))
	for _, v := range data.Files {
		files = append(files, file(v))
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"files": files, "maxBytes": data.Policy.MaxBytes, "allowedMimeTypes": data.Policy.AllowedMIMEs, "maxFiles": data.Policy.MaxFiles, "uploadAvailable": data.UploadAvailable}})
}
