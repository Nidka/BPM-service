// Package normativehttp expone el buscador de normativa (RF-46/47). No registra las búsquedas en logs.
package normativehttp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/normative/application"
	"github.com/Nidka/BPM-service/internal/features/normative/domain"
	"github.com/go-chi/chi/v5"
)

// maxDocumentBody admite 400 000 caracteres de hasta 4 bytes más el resto del cuerpo.
const maxDocumentBody = 1_700_000

// Handler traduce HTTP a los casos de uso.
type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// Register monta las rutas bajo /normative.
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/normative/status", h.status)
		r.Get("/normative/search", h.search)
		r.Get("/normative/documents", h.documents)
		r.Post("/normative/documents", h.create)
		r.Get("/normative/documents/{documentID}", h.document)
		r.Put("/normative/documents/{documentID}", h.update)
		r.Post("/normative/documents/{documentID}/archive", h.archive)
		r.Post("/normative/documents/{documentID}/restore", h.restore)
	})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene permiso para esta operación."})
	case errors.Is(err, domain.ErrNotFound):
		response.Error(w, 404, id, response.APIError{Code: "NORMATIVE_DOCUMENT_NOT_FOUND", Message: "El documento ya no está disponible."})
	default:
		if h.logger != nil {
			h.logger.Error("normative request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
		}
		response.InternalError(w, id)
	}
}

type documentDTO struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Source     string `json:"source"`
	Content    string `json:"content,omitempty"`
	Status     string `json:"status"`
	Characters int    `json:"characters"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

func toDTO(d domain.Document, withContent bool) documentDTO {
	out := documentDTO{ID: d.ID, Title: d.Title, Source: d.Source, Status: d.Status, Characters: d.Characters,
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: d.UpdatedAt.UTC().Format(time.RFC3339)}
	if withContent {
		out.Content = d.Content
	}
	return out
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	count, err := h.service.Status(r.Context(), user)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"documents": count}})
}

type resultDTO struct {
	DocumentID     string `json:"documentId"`
	DocumentTitle  string `json:"documentTitle"`
	DocumentSource string `json:"documentSource"`
	Text           string `json:"text"`
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if len(q) != 1 || len(q["q"]) != 1 {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Envía solo el parámetro q."})
		return
	}
	results, err := h.service.Search(r.Context(), user, q.Get("q"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]resultDTO, 0, len(results))
	for _, v := range results {
		items = append(items, resultDTO{DocumentID: v.DocumentID, DocumentTitle: v.DocumentTitle, DocumentSource: v.DocumentSource, Text: v.Text})
	}
	response.JSON(w, 200, map[string]any{"data": items})
}

func (h *Handler) documents(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = domain.StatusActive
	}
	docs, err := h.service.Documents(r.Context(), user, status)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]documentDTO, 0, len(docs))
	for _, d := range docs {
		items = append(items, toDTO(d, false))
	}
	response.JSON(w, 200, map[string]any{"data": items})
}

func (h *Handler) document(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	d, err := h.service.Document(r.Context(), user, chi.URLParam(r, "documentID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": toDTO(d, true)})
}

type documentBody struct {
	Title   string `json:"title"`
	Source  string `json:"source"`
	Content string `json:"content"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	var body documentBody
	if !request.DecodeStrictJSON(w, r, &body, maxDocumentBody) {
		return
	}
	d, err := h.service.Create(r.Context(), user, domain.DocumentInput{Title: body.Title, Source: body.Source, Content: body.Content})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 201, map[string]any{"data": toDTO(d, false)})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	var body documentBody
	if !request.DecodeStrictJSON(w, r, &body, maxDocumentBody) {
		return
	}
	d, err := h.service.Update(r.Context(), user, chi.URLParam(r, "documentID"), domain.DocumentInput{Title: body.Title, Source: body.Source, Content: body.Content})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": toDTO(d, false)})
}

func (h *Handler) archive(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, domain.StatusArchived)
}
func (h *Handler) restore(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, domain.StatusActive)
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	d, err := h.service.SetStatus(r.Context(), user, chi.URLParam(r, "documentID"), status)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": toDTO(d, false)})
}
