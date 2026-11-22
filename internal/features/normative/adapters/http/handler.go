// Package normativehttp expone el agente normativo (RF-46/47). No registra preguntas ni respuestas en logs.
package normativehttp

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
	"github.com/Nidka/BPM-service/internal/features/normative/application"
	"github.com/Nidka/BPM-service/internal/features/normative/domain"
	"github.com/go-chi/chi/v5"
)

const (
	maxQuestionBody = 8 << 10
	// maxDocumentBody admite 400 000 caracteres de hasta 4 bytes más el resto del cuerpo.
	maxDocumentBody = 1_700_000
	// answerTimeout acota la espera del modelo con la normativa completa.
	answerTimeout = 2 * time.Minute
)

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
		r.Post("/normative/questions", h.ask)
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
	case errors.Is(err, domain.ErrDisabled):
		response.Error(w, 503, id, response.APIError{Code: "NORMATIVE_ASSISTANT_DISABLED", Message: "El asistente de normativa aún no está activado."})
	case errors.Is(err, domain.ErrNoDocuments):
		response.Error(w, 409, id, response.APIError{Code: "NORMATIVE_NO_DOCUMENTS", Message: "Todavía no hay normativa cargada para consultar."})
	case errors.Is(err, domain.ErrRateLimited):
		response.Error(w, 429, id, response.APIError{Code: "NORMATIVE_RATE_LIMITED", Message: fmt.Sprintf("Llegaste al límite de %d consultas por hora. Intenta más tarde.", domain.QuestionsPerHour)})
	case errors.Is(err, domain.ErrTooLarge):
		response.Error(w, 422, id, response.APIError{Code: "NORMATIVE_TOO_LARGE", Message: "La normativa activa superaría el límite. Archiva un documento antes de agregar otro."})
	case errors.Is(err, domain.ErrUnavailable):
		response.Error(w, 503, id, response.APIError{Code: "NORMATIVE_UNAVAILABLE", Message: "El asistente no respondió. Intenta de nuevo en unos minutos."})
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
	s, err := h.service.Status(r.Context(), user)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"enabled": s.Enabled, "documents": s.Documents,
		"questionsPerHour": domain.QuestionsPerHour}})
}

type questionBody struct {
	Question string `json:"question"`
}

func (h *Handler) ask(w http.ResponseWriter, r *http.Request) {
	user, ok := request.VerifiedUID(w, r)
	if !ok {
		return
	}
	var body questionBody
	if !request.DecodeStrictJSON(w, r, &body, maxQuestionBody) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
	defer cancel()
	answer, err := h.service.Ask(ctx, user, body.Question)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	citations := answer.Citations
	if citations == nil {
		citations = []domain.Citation{}
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"answer": answer.Text, "citations": citations, "supported": !answer.Declined}})
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
