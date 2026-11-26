package organizationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httprequest "github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
)

// registerAccounts añade la gestión de cuentas, invitaciones y dispositivos propios.
func (h *Handler) registerAccounts(r chi.Router) {
	r.Patch("/organizations/{organizationID}", h.updateOrganization)
	r.Patch("/organizations/{organizationID}/locations/{locationID}", h.updateLocation)
	r.Patch("/organizations/{organizationID}/members/{userID}", h.updateMember)
	r.Post("/organizations/{organizationID}/members/{userID}/deactivate", h.deactivateMember)
	r.Post("/organizations/{organizationID}/members/{userID}/reactivate", h.reactivateMember)
	r.Post("/organizations/{organizationID}/members/{userID}/transfer-admin", h.transferAdmin)
	r.Get("/organizations/{organizationID}/invitations", h.invitationsPage)
	r.Post("/organizations/{organizationID}/invitations", h.invite)
	r.Post("/organizations/{organizationID}/invitations/{invitationID}/resend", h.resendInvitation)
	r.Post("/organizations/{organizationID}/invitations/{invitationID}/revoke", h.revokeInvitation)
	r.Post("/invitations/accept", h.acceptInvitation)
	r.Get("/me/devices", h.myDevices)
	r.Delete("/me/devices/{deviceID}", h.revokeDevice)
}

// accountError traduce los errores de gestión de cuentas; devuelve false si no es uno de ellos.
func (h *Handler) accountError(w http.ResponseWriter, id string, e error) bool {
	type mapping struct {
		err    error
		status int
		code   string
		msg    string
	}
	for _, m := range []mapping{
		{domain.ErrNotFound, 404, "RESOURCE_NOT_FOUND", "El recurso no existe en esta organización."},
		{domain.ErrVersionConflict, 409, "VERSION_CONFLICT", "El registro cambió desde que lo abriste. Actualiza la información y vuelve a intentar."},
		{domain.ErrLastAdmin, 409, "LAST_ADMIN_REQUIRED", "La organización debe conservar al menos un administrador activo."},
		{domain.ErrMemberExists, 409, "MEMBER_ALREADY_EXISTS", "Esa persona ya pertenece a la organización."},
		{domain.ErrMemberInactive, 409, "MEMBER_INACTIVE", "La persona está inactiva. Reactívala antes de continuar."},
		{domain.ErrInvitationPending, 409, "INVITATION_ALREADY_PENDING", "Ya existe una invitación pendiente para ese correo."},
		{domain.ErrInvitationNotFound, 404, "INVITATION_NOT_FOUND", "La invitación no existe o no es válida."},
		{domain.ErrInvitationExpired, 409, "INVITATION_EXPIRED", "La invitación venció. Pide que te la reenvíen."},
		{domain.ErrInvitationUsed, 409, "INVITATION_ALREADY_USED", "La invitación ya fue utilizada."},
		{domain.ErrInvitationRevoked, 409, "INVITATION_REVOKED", "La invitación fue revocada."},
		{domain.ErrInvitationEmail, 403, "INVITATION_EMAIL_MISMATCH", "La invitación fue enviada a otro correo."},
		{domain.ErrInvitationNotOpen, 409, "INVITATION_NOT_PENDING", "La invitación ya no está pendiente."},
		{domain.ErrResendTooSoon, 429, "INVITATION_RESEND_TOO_SOON", "Espera un minuto antes de reenviar la invitación."},
	} {
		if errors.Is(e, m.err) {
			response.Error(w, m.status, id, response.APIError{Code: m.code, Message: m.msg})
			return true
		}
	}
	return false
}

// statusParam separa el parámetro status del resto de la consulta de paginación.
// Parámetros repetidos devuelven pagination.ErrInvalid.
func statusParam(raw string) (rest, status string, err error) {
	q, e := url.ParseQuery(raw)
	if e != nil {
		return "", "", pagination.ErrInvalid
	}
	if v, ok := q["status"]; ok {
		if len(v) != 1 {
			return "", "", pagination.ErrInvalid
		}
		status = v[0]
		q.Del("status")
	}
	return q.Encode(), status, nil
}

func (h *Handler) pageRequestStatus(w http.ResponseWriter, r *http.Request) (string, string, string, pagination.Request, bool) {
	uid, ok := h.actor(w, r)
	if !ok {
		return "", "", "", pagination.Request{}, false
	}
	rest, status, e := statusParam(r.URL.RawQuery)
	var page pagination.Request
	if e == nil {
		page, e = httprequest.Pagination(rest)
	}
	if e != nil {
		h.fail(w, r, e)
		return "", "", "", page, false
	}
	return uid, chi.URLParam(r, "organizationID"), status, page, true
}

func userParam(r *http.Request) string {
	id := chi.URLParam(r, "userID")
	if len(id) > 128 {
		return ""
	}
	return id
}

// ---------------------------------------------------------------- organización y sedes

func (h *Handler) updateOrganization(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v organizationDTO
	if !decode(w, r, &v) {
		return
	}
	o, e := h.service.UpdateOrganization(r.Context(), uid, chi.URLParam(r, "organizationID"), domain.Organization{LegalName: v.LegalName, TradeName: v.TradeName, TaxID: v.TaxID, BusinessType: v.BusinessType, ContactName: v.ContactName, ContactPhone: v.ContactPhone, ContactEmail: v.ContactEmail}, v.RowVersion)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": organization(o)})
}

func (h *Handler) updateLocation(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v locationDTO
	if !decode(w, r, &v) {
		return
	}
	l, e := h.service.UpdateLocation(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "locationID"), domain.Location{Name: v.Name, Address: v.Address, District: v.District, ContactPhone: v.ContactPhone, Timezone: v.Timezone, Status: v.Status}, v.RowVersion)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": location(l)})
}

// ---------------------------------------------------------------- miembros

func (h *Handler) updateMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v struct {
		Role        string   `json:"role"`
		LocationIDs []string `json:"locationIds"`
		RowVersion  int      `json:"rowVersion"`
	}
	if !decode(w, r, &v) {
		return
	}
	m, e := h.service.UpdateMember(r.Context(), uid, chi.URLParam(r, "organizationID"), userParam(r), application.MemberUpdate{Role: v.Role, LocationIDs: v.LocationIDs, RowVersion: v.RowVersion})
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": member(m)})
}

type versionBody struct {
	RowVersion int `json:"rowVersion"`
}

func (h *Handler) memberTransition(w http.ResponseWriter, r *http.Request, run func(uid, org, target string, version int) (domain.Member, error)) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v versionBody
	if !decode(w, r, &v) {
		return
	}
	m, e := run(uid, chi.URLParam(r, "organizationID"), userParam(r), v.RowVersion)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": member(m)})
}

func (h *Handler) deactivateMember(w http.ResponseWriter, r *http.Request) {
	h.memberTransition(w, r, func(uid, org, target string, version int) (domain.Member, error) {
		return h.service.DeactivateMember(r.Context(), uid, org, target, version)
	})
}

func (h *Handler) reactivateMember(w http.ResponseWriter, r *http.Request) {
	h.memberTransition(w, r, func(uid, org, target string, version int) (domain.Member, error) {
		return h.service.ReactivateMember(r.Context(), uid, org, target, version)
	})
}

func (h *Handler) transferAdmin(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v struct {
		RowVersion       int      `json:"rowVersion"`
		ActorRole        string   `json:"actorRole"`
		ActorLocationIDs []string `json:"actorLocationIds"`
	}
	if !decode(w, r, &v) {
		return
	}
	next, former, e := h.service.TransferAdmin(r.Context(), uid, chi.URLParam(r, "organizationID"), userParam(r), application.TransferInput{RowVersion: v.RowVersion, ActorRole: v.ActorRole, ActorLocationIDs: v.ActorLocationIDs})
	if e != nil {
		h.fail(w, r, e)
		return
	}
	var previous any
	if former.ID != "" {
		previous = member(former)
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"newAdmin": member(next), "formerAdmin": previous}})
}

// ---------------------------------------------------------------- invitaciones

type invitationDTO struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	LocationIDs []string `json:"locationIds"`
	Status      string   `json:"status"`
	ExpiresAt   string   `json:"expiresAt"`
	LastSentAt  string   `json:"lastSentAt"`
	CreatedAt   string   `json:"createdAt"`
	SendCount   int      `json:"sendCount"`
	RowVersion  int      `json:"rowVersion"`
}

func invitation(v domain.Invitation) invitationDTO {
	ids := v.LocationIDs
	if ids == nil {
		ids = []string{}
	}
	return invitationDTO{v.ID, v.Email, v.Role, ids, v.Status, iso(v.ExpiresAt), iso(v.LastSentAt), iso(v.CreatedAt), v.SendCount, v.RowVersion}
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// issuedInvitationDTO añade el enlace para compartir; solo lo usan crear y reenviar.
// AcceptURL "null" (sin URL configurada) o la URL; Token solo viaja sin URL.
type issuedInvitationDTO struct {
	invitationDTO
	AcceptURL json.RawMessage `json:"acceptUrl,omitempty"`
	Token     string          `json:"token,omitempty"`
}

func issued(v domain.Invitation, s application.InvitationShare) any {
	if !s.Issued {
		return invitation(v)
	}
	out := issuedInvitationDTO{invitationDTO: invitation(v), AcceptURL: json.RawMessage("null"), Token: s.Token}
	if s.AcceptURL != "" {
		b, _ := json.Marshal(s.AcceptURL)
		out.AcceptURL = b
	}
	return out
}

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v struct {
		ID          string   `json:"id"`
		Email       string   `json:"email"`
		Role        string   `json:"role"`
		LocationIDs []string `json:"locationIds"`
	}
	if !decode(w, r, &v) {
		return
	}
	inv, share, created, e := h.service.InviteShared(r.Context(), uid, chi.URLParam(r, "organizationID"), application.InvitationInput{ID: v.ID, Email: v.Email, Role: v.Role, LocationIDs: v.LocationIDs})
	if e != nil {
		h.fail(w, r, e)
		return
	}
	status := 200
	if created {
		status = 201
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, status, map[string]any{"data": issued(inv, share)})
}

func (h *Handler) invitationsPage(w http.ResponseWriter, r *http.Request) {
	uid, org, status, p, ok := h.pageRequestStatus(w, r)
	if !ok {
		return
	}
	v, e := h.service.InvitationsPage(r.Context(), uid, org, status, p)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	out := []invitationDTO{}
	for _, i := range v.Items {
		out = append(out, invitation(i))
	}
	pageResponse(w, out, v.NextToken)
}

func (h *Handler) resendInvitation(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	inv, share, e := h.service.ResendShared(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "invitationID"))
	if e != nil {
		h.fail(w, r, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, 200, map[string]any{"data": issued(inv, share)})
}

func (h *Handler) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	h.invitationAction(w, r, h.service.RevokeInvitation)
}

func (h *Handler) invitationAction(w http.ResponseWriter, r *http.Request, run func(ctx context.Context, uid, org, id string) (domain.Invitation, error)) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	inv, e := run(r.Context(), uid, chi.URLParam(r, "organizationID"), chi.URLParam(r, "invitationID"))
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": invitation(inv)})
}

// previewInvitation es público: devuelve organización, rol, correo y vencimiento de una invitación vigente para que
// el cliente muestre «Te invitaron a …» y precargue el correo. Cuerpo máximo 1 KiB; el token nunca se registra.
func (h *Handler) previewInvitation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var v struct {
		Token string `json:"token"`
	}
	if !httprequest.DecodeJSON(w, r, &v, 1024) {
		return
	}
	out, e := h.service.PreviewInvitation(r.Context(), v.Token)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"organizationName": out.OrganizationName, "role": out.Role, "email": out.Email, "expiresAt": iso(out.ExpiresAt), "status": out.Status}})
}

func (h *Handler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	identity, _ := middleware.IdentityFromContext(r.Context())
	var v struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &v) {
		return
	}
	out, e := h.service.AcceptInvitation(r.Context(), uid, identity.Email, v.Token)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"organizationId": out.OrganizationID, "role": out.Role, "locationIds": out.LocationIDs}})
}

// ---------------------------------------------------------------- dispositivos

type deviceDTO struct {
	ID               string  `json:"id"`
	OrganizationID   string  `json:"organizationId"`
	OrganizationName string  `json:"organizationName"`
	Platform         string  `json:"platform"`
	Status           string  `json:"status"`
	LastSeenAt       *string `json:"lastSeenAt"`
	CreatedAt        string  `json:"createdAt"`
}

func device(v domain.Device) deviceDTO {
	var seen *string
	if v.LastSeenAt != nil {
		s := iso(*v.LastSeenAt)
		seen = &s
	}
	return deviceDTO{v.ID, v.OrganizationID, v.OrganizationName, v.Platform, v.Status, seen, iso(v.CreatedAt)}
}

func (h *Handler) myDevices(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	p, e := httprequest.Pagination(r.URL.RawQuery, "pageSize", "pageToken")
	if e != nil {
		h.fail(w, r, e)
		return
	}
	v, e := h.service.MyDevices(r.Context(), uid, p)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	out := []deviceDTO{}
	for _, d := range v.Items {
		out = append(out, device(d))
	}
	pageResponse(w, out, v.NextToken)
}

func (h *Handler) revokeDevice(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength > 0 {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "La solicitud no admite cuerpo ni parámetros."})
		return
	}
	d, e := h.service.RevokeMyDevice(r.Context(), uid, chi.URLParam(r, "deviceID"))
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": device(d)})
}
