package incidentshttp

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (h *Handler) catalogs(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	_, _, q, ok := h.query(w, r, false, false)
	if !ok {
		return
	}
	v, e := h.service.Catalogs(r.Context(), u, o, q)
	page(h, w, r, v, e, catalog)
}
func (h *Handler) incidents(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	site, status, q, ok := h.query(w, r, true, false)
	if !ok {
		return
	}
	v, e := h.service.Incidents(r.Context(), u, o, site, status, q)
	page(h, w, r, v, e, summary)
}
func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return
	}
	_, _, _, ok = h.query(w, r, false, false)
	if !ok {
		return
	}
	v, e := h.service.Detail(r.Context(), u, o, chi.URLParam(r, "incidentID"))
	result(h, w, r, detail(v), e)
}
func (h *Handler) assignees(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	_, _, q, ok := h.query(w, r, false, false)
	if !ok {
		return
	}
	v, e := h.service.Assignees(r.Context(), u, o, chi.URLParam(r, "incidentID"), q)
	page(h, w, r, v, e, assignee)
}
func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	_, _, q, ok := h.query(w, r, false, true)
	if !ok {
		return
	}
	v, e := h.service.History(r.Context(), u, o, chi.URLParam(r, "incidentID"), q)
	page(h, w, r, v, e, event)
}
