package incidentshttp

import (
	"net/http"
	"strings"

	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
)

type staffMessageInput struct {
	ID         string `json:"id"`
	LocationID string `json:"locationId"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
}

func (h *Handler) staffMessage(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	v, ok := decode[staffMessageInput](w, r)
	if !ok {
		return
	}
	out, e := h.service.SendStaffMessage(r.Context(), u, o, r.Header.Get("Idempotency-Key"), domain.StaffMessage{ID: v.ID, LocationID: v.LocationID, Kind: v.Kind, Text: v.Text})
	result(h, w, r, map[string]any{"id": out.ID, "locationId": out.LocationID, "kind": out.Kind, "text": out.Text, "createdAt": out.CreatedAt}, e)
}

func (h *Handler) supervisorContact(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if len(q) != 1 || len(q["locationId"]) != 1 {
		h.fail(w, r, &domain.ValidationError{Fields: map[string]string{"locationId": "Indica la sede."}})
		return
	}
	c, e := h.service.SupervisorContact(r.Context(), u, o, strings.ToLower(q.Get("locationId")))
	var data map[string]any
	if c.WhatsApp != "" {
		data = map[string]any{"name": c.Name, "role": c.Role, "whatsapp": c.WhatsApp}
	} else {
		data = map[string]any{"name": nil, "role": nil, "whatsapp": nil}
	}
	result(h, w, r, data, e)
}
