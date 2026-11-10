// Package traininghttp expone la Academia BPM por HTTP. Los handlers validan
// formato, invocan un caso de uso y traducen el resultado; no contienen reglas
// de negocio ni abren transacciones.
package traininghttp

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/go-chi/chi/v5"
)

// maxBody limita el JSON de cada solicitud; el límite global del servidor también aplica.
const maxBody = 1 << 20

// Handler traduce HTTP a los casos de uso de la Academia.
type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

// New crea el handler.
func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// Register monta las rutas bajo /organizations/{organizationID}/training con autenticación.
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		const base = "/organizations/{organizationID}/training"
		r.Get(base+"/courses", h.listCourses)
		r.Post(base+"/courses", h.createCourse)
		r.Get(base+"/courses/{courseID}", h.course)
		r.Post(base+"/courses/{courseID}/lessons/{lessonID}/video", h.lessonVideo)
		r.Put(base+"/courses/{courseID}/progress", h.saveProgress)
		r.Post(base+"/courses/{courseID}/study/answers", h.study)
		r.Post(base+"/courses/{courseID}/attempts", h.attempt)
		r.Get(base+"/courses/{courseID}/draft", h.getDraft)
		r.Put(base+"/courses/{courseID}/draft", h.putDraft)
		r.Post(base+"/courses/{courseID}/publish", h.publish)
		r.Post(base+"/videos/uploads", h.reserveVideo)
		r.Post(base+"/videos/{mediaID}/confirm", h.confirmVideo)
	})
}

// fail traduce errores del dominio a códigos públicos estables, sin filtrar detalles internos.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	var answers *domain.AnswersError
	var incomplete *domain.IncompleteError
	notFound := func(code, message string) {
		response.Error(w, 404, id, response.APIError{Code: code, Message: message})
	}
	conflict := func(code, message string) {
		response.Error(w, 409, id, response.APIError{Code: code, Message: message})
	}
	switch {
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.As(err, &answers):
		response.Error(w, 422, id, response.APIError{Code: "TRAINING_ANSWERS_INVALID", Message: "Responde cada pregunta exactamente una vez con una opción válida.", Fields: answers.Fields})
	case errors.As(err, &incomplete):
		response.Error(w, 422, id, response.APIError{Code: "TRAINING_DRAFT_INCOMPLETE", Message: "El borrador no está listo para publicarse.", Fields: incomplete.Fields})
	case errors.Is(err, domain.ErrVideoInvalid):
		response.Error(w, 422, id, response.APIError{Code: "TRAINING_VIDEO_INVALID", Message: "El archivo cargado no coincide con el video reservado."})
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene permiso para esta operación."})
	case errors.Is(err, domain.ErrCourseNotFound):
		notFound("TRAINING_COURSE_NOT_FOUND", "El curso no está disponible en esta empresa.")
	case errors.Is(err, domain.ErrLessonNotFound):
		notFound("TRAINING_LESSON_NOT_FOUND", "La lección no pertenece a la versión publicada del curso.")
	case errors.Is(err, domain.ErrQuestionNotFound):
		notFound("TRAINING_QUESTION_NOT_FOUND", "La pregunta no pertenece a la versión publicada del curso.")
	case errors.Is(err, domain.ErrVideoNotFound):
		notFound("TRAINING_VIDEO_NOT_FOUND", "El video no está disponible.")
	case errors.Is(err, domain.ErrDraftNotFound):
		notFound("TRAINING_DRAFT_NOT_FOUND", "El curso no tiene un borrador pendiente.")
	case errors.Is(err, domain.ErrDraftConflict):
		conflict("TRAINING_DRAFT_CONFLICT", "El borrador cambió. Recárgalo antes de guardar.")
	case errors.Is(err, domain.ErrCourseCodeTaken):
		conflict("TRAINING_COURSE_CODE_TAKEN", "Ya existe un curso con ese código en la empresa.")
	case errors.Is(err, domain.ErrVersionOutdated):
		conflict("TRAINING_VERSION_OUTDATED", "El curso se actualizó. Recarga la versión vigente.")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		conflict("IDEMPOTENCY_CONFLICT", "Esta operación ya se usó con otro contenido.")
	case errors.Is(err, domain.ErrVideoConflict):
		conflict("TRAINING_VIDEO_CONFLICT", "El video ya existe con otros datos o no admite esta operación.")
	case errors.Is(err, domain.ErrVideoNotReady):
		conflict("TRAINING_VIDEO_NOT_READY", "Aún no se encontró el archivo cargado. Reintenta la confirmación.")
	case errors.Is(err, domain.ErrUnavailable):
		response.Error(w, 503, id, response.APIError{Code: "STORAGE_UNAVAILABLE", Message: "El almacenamiento de videos no está disponible."})
	default:
		if h.logger != nil {
			h.logger.Error("training request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
		}
		response.InternalError(w, id)
	}
}

// identity extrae el usuario verificado y el tenant de la ruta (que se vuelve a
// autorizar contra la membresía en el caso de uso).
func (h *Handler) identity(w http.ResponseWriter, r *http.Request) (user, org string, ok bool) {
	user, ok = request.VerifiedUID(w, r)
	if !ok {
		return "", "", false
	}
	org = chi.URLParam(r, "organizationID")
	if !domain.ValidOrganization(org) {
		h.fail(w, r, domain.ErrCourseNotFound)
		return "", "", false
	}
	return user, org, true
}

func invalidRequest(w http.ResponseWriter, r *http.Request) {
	response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura y el tamaño de la solicitud."})
}

// emptyRequest exige una solicitud sin query ni cuerpo (GET y POST sin payload).
func emptyRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return false
	}
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(b) > 0 {
			invalidRequest(w, r)
			return false
		}
	}
	return true
}

func data(w http.ResponseWriter, status int, value any) {
	response.JSON(w, status, map[string]any{"data": value})
}

// noStore evita que proxies o navegadores conserven URL firmadas.
func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }
