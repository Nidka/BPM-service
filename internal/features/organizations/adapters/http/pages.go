package organizationhttp

import (
	httprequest "github.com/Nidka/BPM-service/internal/core/httpserver/request"

	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func pageResponse[T any](w http.ResponseWriter, items []T, next string) {
	response.JSON(w, 200, map[string]any{"data": items, "pagination": map[string]string{"nextPageToken": next}})
}
func (h *Handler) pageRequest(w http.ResponseWriter, r *http.Request) (string, string, pagination.Request, bool) {
	uid, ok := h.actor(w, r)
	if !ok {
		return "", "", pagination.Request{}, false
	}
	page, e := httprequest.Pagination(r.URL.RawQuery)
	if e != nil {
		h.fail(w, r, e)
		return "", "", page, false
	}
	return uid, chi.URLParam(r, "organizationID"), page, true
}
func (h *Handler) locationsPage(w http.ResponseWriter, r *http.Request) {
	uid, org, status, p, ok := h.pageRequestStatus(w, r)
	if !ok {
		return
	}
	v, e := h.service.LocationsByStatus(r.Context(), uid, org, status, p)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	out := []locationDTO{}
	for _, l := range v.Items {
		out = append(out, location(l))
	}
	pageResponse(w, out, v.NextToken)
}
func (h *Handler) membersPage(w http.ResponseWriter, r *http.Request) {
	uid, org, status, p, ok := h.pageRequestStatus(w, r)
	if !ok {
		return
	}
	v, e := h.service.MembersByStatus(r.Context(), uid, org, status, p)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	out := []memberDTO{}
	for _, m := range v.Items {
		out = append(out, member(m))
	}
	pageResponse(w, out, v.NextToken)
}
func (h *Handler) entriesPage(resource string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, org, p, ok := h.pageRequest(w, r)
		if !ok {
			return
		}
		v, e := h.service.EntriesPage(r.Context(), uid, org, resource, p)
		if e != nil {
			h.fail(w, r, e)
			return
		}
		pageResponse(w, entries(v.Items), v.NextToken)
	}
}
