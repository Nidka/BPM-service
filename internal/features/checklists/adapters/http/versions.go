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

type versionDTO struct {
	ID          string     `json:"id"`
	TemplateID  string     `json:"templateId"`
	Number      int        `json:"versionNumber"`
	Status      string     `json:"status"`
	ChangeNote  string     `json:"changeNote"`
	ItemCount   int        `json:"itemCount"`
	PublishedAt *time.Time `json:"publishedAt"`
}

func (h *Handler) versions(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	p, err := httprequest.Pagination(r.URL.RawQuery, "pageSize", "pageToken")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.service.TemplateVersions(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "templateID"), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]versionDTO, 0, len(page.Items))
	for _, v := range page.Items {
		out = append(out, versionDTO{v.ID, v.TemplateID, v.Number, v.Status, v.ChangeNote, v.ItemCount, v.PublishedAt})
	}
	response.JSON(w, 200, map[string]any{"data": out, "pagination": map[string]string{"nextPageToken": page.NextToken}})
}

func (h *Handler) version(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	v, err := h.service.TemplateVersion(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "templateID"), chi.URLParam(r, "versionID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": template(v)})
}

func (h *Handler) publishVersion(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input struct {
		VersionID         string    `json:"versionId"`
		BaseVersionNumber int       `json:"baseVersionNumber"`
		ChangeNote        string    `json:"changeNote"`
		Items             []itemDTO `json:"items"`
	}
	if !decode(w, r, &input) {
		return
	}
	items := make([]domain.Item, 0, len(input.Items))
	for _, i := range input.Items {
		items = append(items, i.domain())
	}
	v, err := h.service.PublishVersion(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "templateID"),
		domain.NewVersion{VersionID: input.VersionID, BaseVersionNumber: input.BaseVersionNumber, ChangeNote: input.ChangeNote, Items: items})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 201, map[string]any{"data": template(v)})
}
