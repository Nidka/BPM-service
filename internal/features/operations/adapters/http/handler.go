package operationshttp

import (
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	request "github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/operations/application"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

type Handler struct {
	service *application.Service
	logger  *slog.Logger
}

func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service, logger}
}
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/organizations/{organizationID}/suppliers", h.suppliers)
		r.Post("/organizations/{organizationID}/suppliers", h.createSupplier)
		r.Get("/organizations/{organizationID}/products", h.products)
		r.Post("/organizations/{organizationID}/products", h.createProduct)
		r.Put("/organizations/{organizationID}/products/{productID}/temperature-policy", h.policy)
		r.Get("/organizations/{organizationID}/supply-receipts", h.receipts)
		r.Post("/organizations/{organizationID}/supply-receipts", h.receive)
		r.Get("/organizations/{organizationID}/supply-receipts/{receiptID}", h.receipt)
		r.Get("/organizations/{organizationID}/storage-lots", h.lots)
		r.Get("/organizations/{organizationID}/cleaning-plans", h.plans)
		r.Post("/organizations/{organizationID}/cleaning-plans", h.createPlan)
		r.Get("/organizations/{organizationID}/cleaning-executions", h.executions)
		r.Post("/organizations/{organizationID}/cleaning-executions", h.execute)
		r.Get("/organizations/{organizationID}/cleaning-executions/{executionID}", h.execution)
	})
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var invalid *domain.ValidationError
	switch {
	case errors.As(err, &invalid):
		response.ValidationError(w, id, invalid.Fields)
	case errors.Is(err, pagination.ErrInvalid):
		response.Error(w, 400, id, response.APIError{Code: "INVALID_PAGINATION", Message: "Revisa la búsqueda, la sede y la página solicitadas."})
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene permiso para esta operación o sede."})
	case errors.Is(err, domain.ErrNotFound):
		response.Error(w, 404, id, response.APIError{Code: "NOT_FOUND", Message: "El registro no está disponible en esta empresa o sede."})
	case errors.Is(err, domain.ErrConflict):
		response.Error(w, 409, id, response.APIError{Code: "RESOURCE_CONFLICT", Message: "El registro cambió o esta operación ya tiene datos diferentes. Revisa la versión guardada."})
	default:
		if h.logger != nil {
			h.logger.Error("operation request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
		}
		response.Error(w, 500, id, response.APIError{Code: "INTERNAL_ERROR", Message: "No pudimos confirmar la operación. Reintenta con los mismos datos."})
	}
}
func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	uid, ok := request.VerifiedUID(w, r)
	return uid, chi.URLParam(r, "organizationID"), ok
}
func (h *Handler) query(w http.ResponseWriter, r *http.Request, site bool) (string, pagination.Request, bool) {
	q, e := url.ParseQuery(r.URL.RawQuery)
	location := ""
	if e == nil && site {
		values := q["locationId"]
		if len(values) != 1 || !domain.ValidID(values[0]) {
			e = pagination.ErrInvalid
		} else {
			location = values[0]
		}
		q.Del("locationId")
	}
	var p pagination.Request
	if e == nil {
		p, e = request.Pagination(q.Encode())
	}
	if e != nil {
		h.fail(w, r, pagination.ErrInvalid)
		return "", p, false
	}
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(b) > 0 {
			response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Esta consulta no admite contenido adicional."})
			return "", p, false
		}
	}
	return location, p, true
}
func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if r.URL.RawQuery != "" {
		response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Esta operación no admite parámetros adicionales."})
		return v, false
	}
	return v, request.DecodeJSON(w, r, &v, 512<<10)
}
func result[T any](h *Handler, w http.ResponseWriter, r *http.Request, v T, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	response.JSON(w, 200, map[string]any{"data": v})
}
func page[T any, U any](h *Handler, w http.ResponseWriter, r *http.Request, v pagination.Page[T], err error, project func(T) U) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]U, 0, len(v.Items))
	for _, row := range v.Items {
		out = append(out, project(row))
	}
	response.JSON(w, 200, map[string]any{"data": out, "pagination": map[string]string{"nextPageToken": v.NextToken}})
}
func (h *Handler) suppliers(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	_, q, ok := h.query(w, r, false)
	if !ok {
		return
	}
	v, e := h.service.Suppliers(r.Context(), u, o, q)
	page(h, w, r, v, e, supplier)
}
func (h *Handler) products(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	_, q, ok := h.query(w, r, false)
	if !ok {
		return
	}
	v, e := h.service.Products(r.Context(), u, o, q)
	page(h, w, r, v, e, product)
}
func (h *Handler) receipts(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	site, q, ok := h.query(w, r, true)
	if !ok {
		return
	}
	v, e := h.service.Receipts(r.Context(), u, o, site, q)
	page(h, w, r, v, e, receipt)
}
func (h *Handler) lots(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	site, q, ok := h.query(w, r, true)
	if !ok {
		return
	}
	v, e := h.service.Lots(r.Context(), u, o, site, q)
	page(h, w, r, v, e, lot)
}
func (h *Handler) plans(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	site, q, ok := h.query(w, r, true)
	if !ok {
		return
	}
	v, e := h.service.Plans(r.Context(), u, o, site, q)
	page(h, w, r, v, e, plan)
}
func (h *Handler) executions(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	site, q, ok := h.query(w, r, true)
	if !ok {
		return
	}
	v, e := h.service.Executions(r.Context(), u, o, site, q)
	page(h, w, r, v, e, execution)
}
func (h *Handler) receipt(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	if _, _, ok = h.query(w, r, false); !ok {
		return
	}
	v, e := h.service.Receipt(r.Context(), u, o, chi.URLParam(r, "receiptID"))
	result(h, w, r, receipt(v), e)
}
func (h *Handler) execution(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	if _, _, ok = h.query(w, r, false); !ok {
		return
	}
	v, e := h.service.Execution(r.Context(), u, o, chi.URLParam(r, "executionID"))
	result(h, w, r, execution(v), e)
}
func (h *Handler) createSupplier(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[supplierDTO](w, r)
	if !ok {
		return
	}
	v, e := h.service.CreateSupplier(r.Context(), u, o, r.Header.Get("Idempotency-Key"), input.domain())
	result(h, w, r, supplier(v), e)
}
func (h *Handler) createProduct(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[productInput](w, r)
	if !ok {
		return
	}
	v, e := h.service.CreateProduct(r.Context(), u, o, r.Header.Get("Idempotency-Key"), input.domain())
	result(h, w, r, product(v), e)
}
func (h *Handler) policy(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[policyInput](w, r)
	if !ok {
		return
	}
	v, e := h.service.UpdatePolicy(r.Context(), u, o, chi.URLParam(r, "productID"), r.Header.Get("Idempotency-Key"), input.domain())
	result(h, w, r, product(v), e)
}
func (h *Handler) receive(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[receiptInput](w, r)
	if !ok {
		return
	}
	v, e := h.service.Receive(r.Context(), u, o, r.Header.Get("Idempotency-Key"), input.domain())
	result(h, w, r, receipt(v), e)
}
func (h *Handler) createPlan(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[planInput](w, r)
	if !ok {
		return
	}
	v, e := h.service.CreatePlan(r.Context(), u, o, r.Header.Get("Idempotency-Key"), input.domain())
	result(h, w, r, plan(v), e)
}
func (h *Handler) execute(w http.ResponseWriter, r *http.Request) {
	u, o, ok := h.actor(w, r)
	if !ok {
		return
	}
	input, ok := decode[executionInput](w, r)
	if !ok {
		return
	}
	v, e := h.service.Execute(r.Context(), u, o, r.Header.Get("Idempotency-Key"), input.domain())
	result(h, w, r, execution(v), e)
}
