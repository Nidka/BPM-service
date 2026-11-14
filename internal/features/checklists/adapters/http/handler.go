package checklisthttp

import (
	"errors"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"log/slog"
	"net/http"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httprequest "github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/checklists/application"
	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service     *application.Service
	assignments *application.Assignments
	logger      *slog.Logger
}

func New(service *application.Service, assignments *application.Assignments, logger *slog.Logger) *Handler {
	return &Handler{service: service, assignments: assignments, logger: logger}
}

type itemDTO struct {
	ID                        string   `json:"id"`
	SectionName               string   `json:"sectionName"`
	Label                     string   `json:"label"`
	ResponseType              string   `json:"responseType"`
	Unit                      string   `json:"unit,omitempty"`
	DisplayOrder              int      `json:"displayOrder"`
	Required                  bool     `json:"required"`
	MinimumValue              *float64 `json:"minimumValue,omitempty"`
	MaximumValue              *float64 `json:"maximumValue,omitempty"`
	EvidenceRequiredOnFailure bool     `json:"evidenceRequiredOnFailure"`
}

func (v itemDTO) domain() domain.Item {
	return domain.Item{ID: v.ID, SectionName: v.SectionName, Label: v.Label, ResponseType: v.ResponseType,
		Unit: v.Unit, DisplayOrder: v.DisplayOrder, Required: v.Required, MinimumValue: v.MinimumValue,
		MaximumValue: v.MaximumValue, EvidenceRequiredOnFailure: v.EvidenceRequiredOnFailure}
}

func item(v domain.Item) itemDTO {
	return itemDTO{v.ID, v.SectionName, v.Label, v.ResponseType, v.Unit, v.DisplayOrder,
		v.Required, v.MinimumValue, v.MaximumValue, v.EvidenceRequiredOnFailure}
}

type templateDTO struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Area          string    `json:"area"`
	VersionID     string    `json:"versionId"`
	VersionNumber int       `json:"versionNumber,omitempty"`
	Items         []itemDTO `json:"items"`
}

func (v templateDTO) domain() domain.Template {
	items := make([]domain.Item, 0, len(v.Items))
	for _, i := range v.Items {
		items = append(items, i.domain())
	}
	return domain.Template{ID: v.ID, Name: v.Name, Description: v.Description, Area: v.Area, VersionID: v.VersionID, Items: items}
}

func template(v domain.Template) templateDTO {
	items := make([]itemDTO, 0, len(v.Items))
	for _, i := range v.Items {
		items = append(items, item(i))
	}
	return templateDTO{v.ID, v.Name, v.Description, v.Area, v.VersionID, v.VersionNumber, items}
}

type answerDTO struct {
	ItemID           string   `json:"itemId"`
	ComplianceResult string   `json:"complianceResult,omitempty"`
	TextValue        string   `json:"textValue,omitempty"`
	Observation      string   `json:"observation,omitempty"`
	NumericValue     *float64 `json:"numericValue,omitempty"`
}

func (v answerDTO) domain() domain.Answer {
	return domain.Answer{ItemID: v.ItemID, ComplianceResult: v.ComplianceResult, TextValue: v.TextValue,
		Observation: v.Observation, NumericValue: v.NumericValue}
}

type runDTO struct {
	AssignmentID      string         `json:"assignmentId,omitempty"`
	PerformedByName   string         `json:"performedByName"`
	TemplateName      string         `json:"templateName"`
	LocationName      string         `json:"locationName"`
	CreatedAt         time.Time      `json:"createdAt"`
	ID                string         `json:"id"`
	OrganizationID    string         `json:"organizationId"`
	LocationID        string         `json:"locationId"`
	TemplateVersionID string         `json:"templateVersionId"`
	PerformedBy       string         `json:"performedBy"`
	ClientOperationID string         `json:"clientOperationId"`
	ClientCreatedAt   time.Time      `json:"clientCreatedAt"`
	Status            string         `json:"status"`
	RowVersion        int            `json:"rowVersion"`
	CompliancePercent *float64       `json:"compliancePercent,omitempty"`
	SubmittedAt       *time.Time     `json:"submittedAt,omitempty"`
	Items             []itemDTO      `json:"items"`
	Answers           []answerDTO    `json:"answers"`
	EvidenceCounts    map[string]int `json:"evidenceCounts"`
}

func run(v domain.Run) runDTO {
	if v.EvidenceCounts == nil {
		v.EvidenceCounts = map[string]int{}
	}
	items := make([]itemDTO, 0, len(v.Items))
	for _, i := range v.Items {
		items = append(items, item(i))
	}
	answers := make([]answerDTO, 0, len(v.Answers))
	for _, a := range v.Answers {
		answers = append(answers, answerDTO{a.ItemID, a.ComplianceResult, a.TextValue, a.Observation, a.NumericValue})
	}
	return runDTO{v.AssignmentID, v.PerformedByName, v.TemplateName, v.LocationName, v.CreatedAt, v.ID, v.OrganizationID, v.LocationID, v.TemplateVersionID, v.PerformedBy,
		v.ClientOperationID, v.ClientCreatedAt, v.Status, v.RowVersion, v.CompliancePercent, v.SubmittedAt, items, answers, v.EvidenceCounts}
}

func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/organizations/{organizationID}/checklist-assignments", h.assignmentList)
		r.Get("/organizations/{organizationID}/checklist-assignments/{assignmentID}", h.assignmentGet)
		r.Post("/organizations/{organizationID}/checklist-assignments", h.assignmentCreate)
		r.Post("/organizations/{organizationID}/checklist-assignments/{assignmentID}/deactivate", h.assignmentDeactivate)
		r.Get("/organizations/{organizationID}/checklists", h.catalog)
		r.Get("/organizations/{organizationID}/checklists/{templateID}", h.getTemplate)
		r.Post("/organizations/{organizationID}/checklists", h.createTemplate)
		r.Get("/organizations/{organizationID}/checklist-runs", h.history)
		r.Post("/organizations/{organizationID}/checklist-runs", h.createRun)
		r.Get("/organizations/{organizationID}/checklist-runs/{runID}", h.getRun)
		r.Put("/organizations/{organizationID}/checklist-runs/{runID}/answers", h.saveAnswers)
		r.Post("/organizations/{organizationID}/checklist-runs/{runID}/submit", h.submit)
	})
}

func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (string, bool) {
	return httprequest.VerifiedUID(w, r)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	switch {
	case errors.Is(err, domain.ErrAssignmentRequired):
		response.Error(w, 409, id, response.APIError{Code: "ASSIGNMENT_REQUIRED", Message: "Esta plantilla tiene un control programado para la sede. Ábrelo desde Control."})
	case errors.Is(err, domain.ErrAssignmentStarted):
		response.Error(w, 409, id, response.APIError{Code: "ASSIGNMENT_ALREADY_STARTED", Message: "El control de este período ya se inició. Consulta Control para retomarlo o ver su resultado."})
	case errors.Is(err, pagination.ErrInvalid):
		response.Error(w, 400, id, response.APIError{Code: "INVALID_PAGINATION", Message: "La página no es válida. Actualiza la lista y vuelve a intentar."})
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "No tienes acceso a esta organización, sede o acción."})
	case errors.Is(err, domain.ErrNotFound):
		response.Error(w, 404, id, response.APIError{Code: "NOT_FOUND", Message: "El recurso solicitado no existe."})
	case errors.Is(err, domain.ErrVersionConflict):
		response.Error(w, 409, id, response.APIError{Code: "VERSION_CONFLICT", Message: "El borrador cambió. Descarga la última versión antes de sincronizar."})
	case errors.Is(err, domain.ErrConflict):
		response.Error(w, 409, id, response.APIError{Code: "RESOURCE_CONFLICT", Message: "La operación ya existe con otros datos o el recurso no puede modificarse."})
	default:
		h.logger.Error("checklist request failed", "request_id", id, "error_type", "internal")
		response.InternalError(w, id)
	}
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	return httprequest.DecodeJSON(w, r, target, 65536)
}

func (h *Handler) catalog(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	p, err := httprequest.Pagination(r.URL.RawQuery)
	if r.URL.RawQuery == "" {
		p.Size = 100
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	values, err := h.service.CatalogPage(r.Context(), uid, chi.URLParam(r, "organizationID"), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]templateDTO, 0, len(values.Items))
	for _, v := range values.Items {
		out = append(out, template(v))
	}
	response.JSON(w, 200, map[string]any{"data": out, "pagination": map[string]string{"nextPageToken": values.NextToken}})
}

func (h *Handler) createTemplate(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input templateDTO
	if !decode(w, r, &input) {
		return
	}
	v, err := h.service.CreateTemplate(r.Context(), uid, chi.URLParam(r, "organizationID"), input.domain())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 201, map[string]any{"data": template(v)})
}

func (h *Handler) createRun(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input struct {
		AssignmentID      string    `json:"assignmentId"`
		ID                string    `json:"id"`
		LocationID        string    `json:"locationId"`
		TemplateVersionID string    `json:"templateVersionId"`
		ClientOperationID string    `json:"clientOperationId"`
		ClientCreatedAt   time.Time `json:"clientCreatedAt"`
	}
	if !decode(w, r, &input) {
		return
	}
	v, err := h.service.CreateRun(r.Context(), uid, chi.URLParam(r, "organizationID"), domain.Run{
		AssignmentID: input.AssignmentID, ID: input.ID, LocationID: input.LocationID, TemplateVersionID: input.TemplateVersionID,
		ClientOperationID: input.ClientOperationID, ClientCreatedAt: input.ClientCreatedAt})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": run(v)})
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	v, err := h.service.Run(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "runID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": run(v)})
}

func (h *Handler) saveAnswers(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input struct {
		RowVersion int         `json:"rowVersion"`
		Answers    []answerDTO `json:"answers"`
	}
	if !decode(w, r, &input) {
		return
	}
	answers := make([]domain.Answer, 0, len(input.Answers))
	for _, a := range input.Answers {
		answers = append(answers, a.domain())
	}
	v, err := h.service.SaveAnswers(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "runID"), input.RowVersion, r.Header.Get("Idempotency-Key"), answers)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": run(v)})
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input struct {
		RowVersion int `json:"rowVersion"`
	}
	if !decode(w, r, &input) {
		return
	}
	v, err := h.service.Submit(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "runID"), input.RowVersion, r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": run(v)})
}

func (h *Handler) getTemplate(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	v, e := h.service.Template(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "templateID"))
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": template(v)})
}
