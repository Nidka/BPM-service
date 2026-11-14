package checklisthttp

import (
	"net/http"
	"time"

	httprequest "github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
)

type assignedRunDTO struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	PerformedBy     string     `json:"performedBy"`
	PerformedByName string     `json:"performedByName"`
	CreatedAt       time.Time  `json:"createdAt"`
	SubmittedAt     *time.Time `json:"submittedAt"`
}
type assignmentDTO struct {
	ID                string          `json:"id"`
	TemplateID        string          `json:"templateId"`
	TemplateName      string          `json:"templateName"`
	TemplateVersionID string          `json:"templateVersionId"`
	LocationID        string          `json:"locationId"`
	LocationName      string          `json:"locationName"`
	Frequency         string          `json:"frequency"`
	DueTime           string          `json:"dueTime"`
	Timezone          string          `json:"timezone"`
	Active            bool            `json:"active"`
	CreatedAt         time.Time       `json:"createdAt"`
	PeriodStartsAt    *time.Time      `json:"periodStartsAt"`
	PeriodEndsAt      *time.Time      `json:"periodEndsAt"`
	Run               *assignedRunDTO `json:"run"`
}

func assignment(v domain.Assignment) assignmentDTO {
	out := assignmentDTO{ID: v.ID, TemplateID: v.TemplateID, TemplateName: v.TemplateName, TemplateVersionID: v.TemplateVersionID, LocationID: v.LocationID, LocationName: v.LocationName,
		Frequency: v.Frequency, DueTime: v.DueTime, Timezone: v.Timezone, Active: v.Active, CreatedAt: v.CreatedAt, PeriodEndsAt: v.Period.EndsAt}
	if !v.Period.StartsAt.IsZero() {
		out.PeriodStartsAt = &v.Period.StartsAt
	}
	if v.Run != nil {
		r := v.Run
		out.Run = &assignedRunDTO{r.ID, r.Status, r.PerformedBy, r.PerformedByName, r.CreatedAt, r.SubmittedAt}
	}
	return out
}
func (h *Handler) assignmentList(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	p, err := httprequest.Pagination(r.URL.RawQuery)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.assignments.List(r.Context(), uid, chi.URLParam(r, "organizationID"), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]assignmentDTO, 0, len(page.Items))
	for _, v := range page.Items {
		out = append(out, assignment(v))
	}
	response.JSON(w, 200, map[string]any{"data": out, "pagination": map[string]string{"nextPageToken": page.NextToken}})
}
func (h *Handler) assignmentGet(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	v, err := h.assignments.Get(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "assignmentID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": assignment(v)})
}
func (h *Handler) assignmentCreate(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	var input struct {
		ID         string `json:"id"`
		TemplateID string `json:"templateId"`
		LocationID string `json:"locationId"`
		Frequency  string `json:"frequency"`
		DueTime    string `json:"dueTime"`
	}
	if !httprequest.DecodeJSON(w, r, &input, 4096) {
		return
	}
	v, err := h.assignments.Create(r.Context(), uid, chi.URLParam(r, "organizationID"), domain.Assignment{ID: input.ID, TemplateID: input.TemplateID, LocationID: input.LocationID, Frequency: input.Frequency, DueTime: input.DueTime})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": assignment(v)})
}
func (h *Handler) assignmentDeactivate(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	var input struct{}
	if !httprequest.DecodeJSON(w, r, &input, 128) {
		return
	}
	v, err := h.assignments.Deactivate(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "assignmentID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": assignment(v)})
}
