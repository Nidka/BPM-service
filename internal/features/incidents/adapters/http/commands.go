package incidentshttp

import (
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (h *Handler) requiredEvidence(w http.ResponseWriter, r *http.Request, v *bool) bool {
	if v == nil {
		h.fail(w, r, &domain.ValidationError{Fields: map[string]string{"evidenceRequired": "Indica si se requiere evidencia."}})
		return false
	}
	return true
}
func (h *Handler) createCatalog(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	v, ok := decode[catalogInput](w, r)
	if !ok || !h.requiredEvidence(w, r, v.EvidenceRequired) {
		return
	}
	out, e := h.service.CreateCatalog(r.Context(), u, o, r.Header.Get("Idempotency-Key"), v.domain())
	result(h, w, r, catalog(out), e)
}
func (h *Handler) versionCatalog(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	v, ok := decode[versionInput](w, r)
	if !ok || !h.requiredEvidence(w, r, v.EvidenceRequired) {
		return
	}
	out, e := h.service.VersionCatalog(r.Context(), u, o, chi.URLParam(r, "catalogID"), r.Header.Get("Idempotency-Key"), v.domain())
	result(h, w, r, catalog(out), e)
}
func (h *Handler) report(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	v, ok := decode[reportInput](w, r)
	if !ok {
		return
	}
	out, e := h.service.Report(r.Context(), u, o, r.Header.Get("Idempotency-Key"), v.domain())
	result(h, w, r, detail(out), e)
}
func (h *Handler) act(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	v, ok := decode[commandInput](w, r)
	if !ok {
		return
	}
	c, e := v.domain()
	if e != nil {
		h.fail(w, r, e)
		return
	}
	out, e := h.service.Act(r.Context(), u, o, chi.URLParam(r, "incidentID"), r.Header.Get("Idempotency-Key"), c)
	result(h, w, r, detail(out), e)
}
