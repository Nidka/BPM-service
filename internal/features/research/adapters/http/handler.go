// Package researchhttp expone el módulo de investigación por HTTP. Los handlers
// validan formato, invocan un caso de uso y traducen el resultado; no contienen
// reglas de negocio ni abren transacciones.
package researchhttp

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/research/application"
	"github.com/Nidka/BPM-service/internal/features/research/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
)

// maxBody limita el JSON de cada solicitud; el límite global del servidor también aplica.
const maxBody = 1 << 20

// Handler traduce HTTP a los casos de uso de investigación.
type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

// New crea el handler.
func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// Register monta las rutas con autenticación. Participante: bajo la organización
// de su membresía. Investigación: rutas de plataforma bajo /research, protegidas
// por la concesión de investigador (no por roles de empresa).
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		const org = "/organizations/{organizationID}/research"
		r.Get(org+"/participation", h.participation)
		r.Post(org+"/consent", h.consent)
		r.Post(org+"/submissions", h.submit)

		const base = "/research"
		r.Get(base+"/access", h.access)
		r.Get(base+"/access-grants", h.listGrants)
		r.Post(base+"/access-grants", h.grant)
		r.Delete(base+"/access-grants/{userID}", h.revoke)
		r.Get(base+"/consent-documents", h.listConsentDocuments)
		r.Post(base+"/consent-documents", h.publishConsentDocument)
		r.Get(base+"/instruments", h.listInstruments)
		r.Post(base+"/instruments", h.createInstrument)
		r.Post(base+"/instruments/import", h.importInstrument)
		r.Get(base+"/instruments/{instrumentID}", h.getInstrument)
		r.Get(base+"/instruments/{instrumentID}/draft", h.getDraft)
		r.Put(base+"/instruments/{instrumentID}/draft", h.putDraft)
		r.Post(base+"/instruments/{instrumentID}/versions", h.createDraft)
		r.Post(base+"/instruments/{instrumentID}/publish", h.publishDraft)
		r.Get(base+"/organizations", h.listOrganizations)
		r.Get(base+"/results", h.results)
		r.Get(base+"/exports/responses.csv", h.exportCSV)
		r.Get(base+"/needs", h.listNeeds)
		r.Post(base+"/needs", h.addNeed)
	})
}

// fail traduce errores del dominio a códigos públicos estables, sin filtrar detalles internos.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	var answers *domain.AnswersError
	var incomplete *domain.IncompleteError
	send := func(status int, code, message string) {
		response.Error(w, status, id, response.APIError{Code: code, Message: message})
	}
	switch {
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.As(err, &answers):
		response.Error(w, 422, id, response.APIError{Code: "RESEARCH_ANSWERS_INVALID", Message: "Responde cada pregunta exactamente una vez con un valor válido.", Fields: answers.Fields})
	case errors.As(err, &incomplete):
		response.Error(w, 422, id, response.APIError{Code: "RESEARCH_INSTRUMENT_INCOMPLETE", Message: "El borrador no está listo para publicarse.", Fields: incomplete.Fields})
	case errors.Is(err, pagination.ErrInvalid):
		send(400, "INVALID_PAGINATION", "Revisa la página y los filtros solicitados.")
	case errors.Is(err, domain.ErrDenied):
		send(403, "RESEARCH_ACCESS_DENIED", "Tu cuenta no tiene acceso de investigación.")
	case errors.Is(err, domain.ErrNotMember):
		send(403, "ACCESS_DENIED", "Tu cuenta no tiene permiso para esta operación.")
	case errors.Is(err, domain.ErrConsentRequired):
		send(403, "RESEARCH_CONSENT_REQUIRED", "Necesitas un consentimiento informado vigente para participar.")
	case errors.Is(err, domain.ErrConsentVersionStale):
		send(409, "RESEARCH_CONSENT_VERSION_OUTDATED", "El documento de consentimiento cambió. Revisa la versión vigente.")
	case errors.Is(err, domain.ErrConsentUnavailable):
		send(409, "RESEARCH_CONSENT_DOCUMENT_UNAVAILABLE", "Aún no hay un documento de consentimiento publicado.")
	case errors.Is(err, domain.ErrConsentVersionTaken):
		send(409, "RESEARCH_CONSENT_VERSION_TAKEN", "Ya existe un documento con esa versión.")
	case errors.Is(err, domain.ErrNoParticipant):
		send(404, "RESEARCH_PARTICIPANT_NOT_FOUND", "Aún no participas en el estudio.")
	case errors.Is(err, domain.ErrSurveyUnavailable):
		send(404, "RESEARCH_SURVEY_UNAVAILABLE", "No hay un instrumento CAP publicado.")
	case errors.Is(err, domain.ErrVersionOutdated):
		send(409, "RESEARCH_VERSION_OUTDATED", "El instrumento se actualizó. Recarga la versión vigente.")
	case errors.Is(err, domain.ErrDuplicateSubmission):
		send(409, "RESEARCH_SUBMISSION_EXISTS", "Ya enviaste esta aplicación (pretest o postest) para esta versión.")
	case errors.Is(err, domain.ErrIdempotencyConflict):
		send(409, "IDEMPOTENCY_CONFLICT", "Esta operación ya se usó con otro contenido.")
	case errors.Is(err, domain.ErrInstrumentNotFound):
		send(404, "RESEARCH_INSTRUMENT_NOT_FOUND", "El instrumento no existe.")
	case errors.Is(err, domain.ErrVersionNotFound):
		send(404, "RESEARCH_VERSION_NOT_FOUND", "La versión del instrumento no existe.")
	case errors.Is(err, domain.ErrInstrumentCodeTaken):
		send(409, "RESEARCH_INSTRUMENT_CODE_TAKEN", "Ya existe un instrumento con ese código.")
	case errors.Is(err, domain.ErrCAPExists):
		send(409, "RESEARCH_CAP_EXISTS", "Ya existe un instrumento CAP oficial.")
	case errors.Is(err, domain.ErrDraftNotFound):
		send(404, "RESEARCH_DRAFT_NOT_FOUND", "El instrumento no tiene un borrador.")
	case errors.Is(err, domain.ErrDraftExists):
		send(409, "RESEARCH_DRAFT_EXISTS", "El instrumento ya tiene un borrador abierto.")
	case errors.Is(err, domain.ErrDraftConflict):
		send(409, "RESEARCH_DRAFT_CONFLICT", "El borrador cambió. Recárgalo antes de guardar.")
	case errors.Is(err, domain.ErrUserNotFound):
		send(404, "RESEARCH_USER_NOT_FOUND", "No hay un usuario activo con ese correo.")
	case errors.Is(err, domain.ErrGrantNotFound):
		send(404, "RESEARCH_GRANT_NOT_FOUND", "El usuario no tiene una concesión de acceso.")
	case errors.Is(err, domain.ErrOrganizationNotFound):
		send(404, "RESEARCH_ORGANIZATION_NOT_FOUND", "La empresa no existe.")
	case errors.Is(err, domain.ErrExportTooLarge):
		send(422, "RESEARCH_EXPORT_TOO_LARGE", "La exportación supera el límite de filas. Filtra por empresa.")
	default:
		if h.logger != nil {
			h.logger.Error("research request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
		}
		response.InternalError(w, id)
	}
}

func invalidRequest(w http.ResponseWriter, r *http.Request) {
	response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura y el tamaño de la solicitud."})
}

// data responde {"data": ...}. Los datos de investigación nunca se almacenan en caché.
func data(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, status, map[string]any{"data": value})
}

func dataPage[T any](w http.ResponseWriter, page pagination.Page[T], items any) {
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, map[string]any{"data": items, "pagination": map[string]string{"nextPageToken": page.NextToken}})
}

// uid extrae el usuario verificado.
func uid(w http.ResponseWriter, r *http.Request) (string, bool) { return request.VerifiedUID(w, r) }

// query separa filtros conocidos (como máximo un valor) del resto, que debe ser
// paginación (pageSize y pageToken). Parámetros repetidos o desconocidos → 400.
func (h *Handler) query(w http.ResponseWriter, r *http.Request, filters ...string) (map[string]string, pagination.Request, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		h.fail(w, r, pagination.ErrInvalid)
		return nil, pagination.Request{}, false
	}
	found := map[string]string{}
	for _, f := range filters {
		if values, ok := q[f]; ok {
			if len(values) != 1 {
				h.fail(w, r, pagination.ErrInvalid)
				return nil, pagination.Request{}, false
			}
			found[f] = values[0]
			q.Del(f)
		}
	}
	p, err := request.Pagination(q.Encode(), "pageSize", "pageToken")
	if err != nil {
		h.fail(w, r, err)
		return nil, p, false
	}
	return found, p, true
}

// emptyQuery exige una solicitud sin query string.
func emptyQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return false
	}
	return true
}
