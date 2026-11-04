package organizationhttp

import (
	"errors"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"log/slog"
	"net/http"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httprequest "github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/Nidka/BPM-service/internal/features/organizations/domain"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service, logger}
}

type organizationDTO struct {
	ID           string `json:"id"`
	LegalName    string `json:"legalName"`
	TradeName    string `json:"tradeName"`
	TaxID        string `json:"taxId"`
	BusinessType string `json:"businessType"`
	ContactName  string `json:"contactName"`
	ContactPhone string `json:"contactPhone"`
	ContactEmail string `json:"contactEmail"`
}

func organization(o domain.Organization) organizationDTO {
	return organizationDTO{o.ID, o.LegalName, o.TradeName, o.TaxID, o.BusinessType, o.ContactName, o.ContactPhone, o.ContactEmail}
}

type locationDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Address      string `json:"address"`
	District     string `json:"district"`
	ContactPhone string `json:"contactPhone"`
	Timezone     string `json:"timezone"`
}
type memberDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	LocationIDs []string `json:"locationIds"`
}
type entryDTO struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status string `json:"status"`
}

func entries(input []domain.Entry) []entryDTO {
	out := []entryDTO{}
	for _, v := range input {
		out = append(out, entryDTO{v.ID, v.Title, v.Detail, v.Status})
	}
	return out
}
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/organizations", h.list)
		r.Post("/organizations", h.create)
		r.Get("/organizations/{organizationID}/locations", h.locationsPage)
		r.Get("/organizations/{organizationID}/members", h.membersPage)
		r.Get("/organizations/{organizationID}/courses", h.entriesPage("courses"))
		r.Get("/organizations/{organizationID}/checklist-summaries", h.entriesPage("checklist-summaries"))
		r.Get("/organizations/{organizationID}/workspace", h.workspace)
		r.Post("/organizations/{organizationID}/locations", h.createLocation)
		r.Post("/organizations/{organizationID}/members", h.addMember)
		r.Post("/organizations/{organizationID}/members/check", h.checkMember)
		r.Post("/organizations/{organizationID}/members/search", h.searchMemberAccounts)
	})
}
func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (string, bool) {
	return httprequest.VerifiedUID(w, r)
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, e error) {
	id := middleware.RequestIDFromContext(r.Context())
	var v *domain.ValidationError
	switch {
	case errors.Is(e, pagination.ErrInvalid):
		response.Error(w, 400, id, response.APIError{Code: "INVALID_PAGINATION", Message: "La página no es válida. Actualiza la lista y vuelve a intentar."})
	case errors.As(e, &v):
		response.ValidationError(w, id, v.Fields)
	case errors.Is(e, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "No tienes acceso a esta organización o acción."})
	case errors.Is(e, domain.ErrConflict):
		response.Error(w, 409, id, response.APIError{Code: "RESOURCE_CONFLICT", Message: "El registro ya existe o cambió. Actualiza la información antes de continuar."})
	case errors.Is(e, domain.ErrMemberUnavailable):
		response.Error(w, 404, id, response.APIError{Code: "MEMBER_NOT_AVAILABLE", Message: "No se encontró una cuenta BPM activa con ese correo."})
	default:
		h.logger.Error("workspace request failed", "request_id", id, "error_type", "internal")
		response.InternalError(w, id)
	}
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	return httprequest.DecodeJSON(w, r, target, 16384)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	page, e := httprequest.Pagination(r.URL.RawQuery)
	if r.URL.RawQuery == "" {
		page.Size = 100
	}
	if e != nil {
		h.fail(w, r, e)
		return
	}
	v, e := h.service.OrganizationsPage(r.Context(), uid, page)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	out := []organizationDTO{}
	for _, o := range v.Items {
		out = append(out, organization(o))
	}
	response.JSON(w, 200, map[string]any{"data": out, "pagination": map[string]any{"nextPageToken": v.NextToken}})
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v organizationDTO
	if !decode(w, r, &v) {
		return
	}
	o, e := h.service.CreateOrganization(r.Context(), uid, domain.Organization{ID: v.ID, LegalName: v.LegalName, TradeName: v.TradeName, TaxID: v.TaxID, BusinessType: v.BusinessType, ContactName: v.ContactName, ContactPhone: v.ContactPhone, ContactEmail: v.ContactEmail})
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": organization(o)})
}
func (h *Handler) workspace(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	p, e := httprequest.Pagination(r.URL.RawQuery, "pageSize")
	if e != nil {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	if r.URL.RawQuery == "" {
		p.Size = 100
	}
	v, e := h.service.WorkspacePage(r.Context(), uid, chi.URLParam(r, "organizationID"), p.Size)
	if e != nil {
		h.fail(w, r, e)
		return
	}
	ls := []locationDTO{}
	ms := []memberDTO{}
	for _, l := range v.Locations {
		ls = append(ls, locationDTO{l.ID, l.Name, l.Address, l.District, l.ContactPhone, l.Timezone})
	}
	for _, m := range v.Members {
		ms = append(ms, memberDTO{m.ID, m.Name, m.Email, m.Role, m.LocationIDs})
	}
	response.JSON(w, 200, map[string]any{"data": map[string]any{"organization": organization(v.Organization), "role": v.Role, "nextPageTokens": v.NextTokens, "totals": v.Totals, "locations": ls, "members": ms, "courses": entries(v.Courses), "checklists": entries(v.Checklists), "incidents": entries(v.Incidents)}})
}
func (h *Handler) createLocation(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v locationDTO
	if !decode(w, r, &v) {
		return
	}
	o, e := h.service.CreateLocation(r.Context(), uid, chi.URLParam(r, "organizationID"), domain.Location{ID: v.ID, Name: v.Name, Address: v.Address, District: v.District, ContactPhone: v.ContactPhone})
	if e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": locationDTO{o.ID, o.Name, o.Address, o.District, o.ContactPhone, o.Timezone}})
}
func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.actor(w, r)
	if !ok {
		return
	}
	var v struct {
		Email       string   `json:"email"`
		Role        string   `json:"role"`
		LocationIDs []string `json:"locationIds"`
	}
	if !decode(w, r, &v) {
		return
	}
	if e := h.service.AddMember(r.Context(), uid, chi.URLParam(r, "organizationID"), v.Email, v.Role, v.LocationIDs); e != nil {
		h.fail(w, r, e)
		return
	}
	response.JSON(w, 200, map[string]any{"data": map[string]string{"status": "ACTIVE"}})
}
