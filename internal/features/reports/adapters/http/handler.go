// Package reportshttp expone los reportes por HTTP. Los handlers validan formato, invocan un
// caso de uso y traducen el resultado; no contienen reglas de negocio ni abren transacciones.
package reportshttp

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/reports/application"
	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/go-chi/chi/v5"
)

// Handler traduce HTTP a los casos de uso de reportes.
type Handler struct {
	service *application.Service
	logger  *slog.Logger
	// renderPDF arma el PDF del historial; sin él la ruta .pdf no se registra.
	renderPDF PDFRenderer
}

// PDFRenderer convierte el contenido del reporte en un archivo PDF.
type PDFRenderer func(domain.ExportDocument) ([]byte, error)

// WithPDF habilita la exportación PDF con el renderizador indicado.
func (h *Handler) WithPDF(render PDFRenderer) *Handler {
	h.renderPDF = render
	return h
}

// New crea el handler.
func New(service *application.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// Register monta las rutas autenticadas bajo /organizations/{organizationID}/reports y la
// verificación pública de constancias, que no requiere autenticación.
func (h *Handler) Register(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Get("/compliance-certificates/{code}", h.verify)
	r.Group(func(r chi.Router) {
		r.Use(auth)
		const base = "/organizations/{organizationID}/reports"
		r.Get(base+"/compliance", h.compliance)
		r.Get(base+"/compliance/export.csv", h.export)
		if h.renderPDF != nil {
			r.Get(base+"/compliance/export.pdf", h.exportPDF)
		}
		r.Get(base+"/summary", h.summary)
		r.Get(base+"/kpis", h.kpis)
		r.Get(base+"/training/courses", h.trainingCourses)
		r.Get(base+"/training/people", h.trainingPeople)
		r.Get(base+"/settings", h.getSettings)
		r.Put(base+"/settings", h.putSettings)
		r.Get(base+"/certificates", h.listCertificates)
		r.Post(base+"/certificates", h.issueCertificate)
	})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	id := middleware.RequestIDFromContext(r.Context())
	var validation *domain.ValidationError
	var threshold *domain.ThresholdError
	switch {
	case errors.As(err, &validation):
		response.ValidationError(w, id, validation.Fields)
	case errors.As(err, &threshold):
		response.Error(w, 422, id, response.APIError{Code: "REPORT_THRESHOLD_NOT_MET", Message: "El cumplimiento promedio del período no alcanza el umbral para emitir la constancia.",
			Fields: map[string]string{"averageCompliance": strconv.FormatFloat(threshold.Average, 'f', 2, 64), "threshold": strconv.FormatFloat(threshold.Threshold, 'f', 2, 64)}})
	case errors.Is(err, domain.ErrNoData):
		response.Error(w, 422, id, response.APIError{Code: "REPORT_NO_DATA", Message: "No hay controles finalizados en el período."})
	case errors.Is(err, domain.ErrExportTooLarge):
		response.Error(w, 422, id, response.APIError{Code: "EXPORT_TOO_LARGE", Message: fmt.Sprintf("La exportación supera %d filas. Reduce el período o filtra por sede.", domain.MaxExportRows)})
	case errors.Is(err, domain.ErrPDFTooLarge):
		response.Error(w, 422, id, response.APIError{Code: "EXPORT_TOO_LARGE", Message: fmt.Sprintf("El PDF admite hasta %d controles. Reduce el período o filtra por sede, o descarga el CSV.", domain.MaxPDFExportRows)})
	case errors.Is(err, domain.ErrDenied):
		response.Error(w, 403, id, response.APIError{Code: "ACCESS_DENIED", Message: "Tu cuenta no tiene permiso para esta operación."})
	case errors.Is(err, domain.ErrLocationNotFound):
		response.Error(w, 404, id, response.APIError{Code: "REPORT_LOCATION_NOT_FOUND", Message: "La sede no está disponible para tu cuenta."})
	case errors.Is(err, domain.ErrCourseNotFound):
		response.Error(w, 404, id, response.APIError{Code: "TRAINING_COURSE_NOT_FOUND", Message: "El curso no está disponible en esta empresa."})
	case errors.Is(err, domain.ErrCertificateNotFound):
		response.Error(w, 404, id, response.APIError{Code: "CERTIFICATE_NOT_FOUND", Message: "No existe una constancia con ese código."})
	case errors.Is(err, pagination.ErrInvalid):
		response.Error(w, 400, id, response.APIError{Code: "INVALID_PAGINATION", Message: "Revisa el tamaño, el cursor y los filtros de la lista."})
	default:
		if h.logger != nil {
			h.logger.Error("reports request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
		}
		response.InternalError(w, id)
	}
}

func (h *Handler) identity(w http.ResponseWriter, r *http.Request) (user, org string, ok bool) {
	user, ok = request.VerifiedUID(w, r)
	if !ok {
		return "", "", false
	}
	org = chi.URLParam(r, "organizationID")
	if !domain.ValidOrganization(org) {
		h.fail(w, r, domain.ErrDenied)
		return "", "", false
	}
	return user, org, true
}

func invalidRequest(w http.ResponseWriter, r *http.Request) {
	response.Error(w, 400, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INVALID_REQUEST", Message: "Revisa la estructura y el tamaño de la solicitud."})
}

// query lee parámetros permitidos sin repetidos ni desconocidos.
func query(r *http.Request, allowed ...string) (url.Values, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, false
	}
	permitted := map[string]bool{}
	for _, k := range allowed {
		permitted[k] = true
	}
	for k, v := range q {
		if len(v) != 1 || !permitted[k] {
			return nil, false
		}
	}
	return q, true
}

func (h *Handler) period(w http.ResponseWriter, r *http.Request) (application.PeriodInput, bool) {
	q, ok := query(r, "from", "to", "locationId")
	if !ok {
		invalidRequest(w, r)
		return application.PeriodInput{}, false
	}
	return application.PeriodInput{From: q.Get("from"), To: q.Get("to"), LocationID: q.Get("locationId")}, true
}

func data(w http.ResponseWriter, status int, value any) {
	response.JSON(w, status, map[string]any{"data": value})
}

func (h *Handler) compliance(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	in, ok := h.period(w, r)
	if !ok {
		return
	}
	out, err := h.service.Compliance(r.Context(), user, org, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, complianceOf(out))
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	in, ok := h.period(w, r)
	if !ok {
		return
	}
	out, err := h.service.Summary(r.Context(), user, org, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, summaryOf(out))
}

func (h *Handler) kpis(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return
	}
	out, err := h.service.KPIs(r.Context(), user, org)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, kpisOf(out))
}

func (h *Handler) trainingCourses(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return
	}
	out, err := h.service.TrainingCourses(r.Context(), user, org)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]trainingCourseDTO, 0, len(out))
	for _, c := range out {
		items = append(items, trainingCourseDTO(c))
	}
	data(w, 200, items)
}

func (h *Handler) trainingPeople(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["courseId"]) > 1 {
		h.fail(w, r, pagination.ErrInvalid)
		return
	}
	courseID := q.Get("courseId")
	q.Del("courseId")
	page, err := request.Pagination(q.Encode(), "pageSize", "pageToken")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out, err := h.service.TrainingPeople(r.Context(), user, org, courseID, page)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]trainingPersonDTO, 0, len(out.Items))
	for _, p := range out.Items {
		items = append(items, personOf(p))
	}
	response.JSON(w, 200, map[string]any{"data": items, "pagination": map[string]string{"nextPageToken": out.NextToken}})
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return
	}
	s, err := h.service.Settings(r.Context(), user, org)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, settingsDTO(s))
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var in settingsRequest
	if !request.DecodeStrictJSON(w, r, &in, 1<<10) {
		return
	}
	if in.WeeklyEmailEnabled == nil || in.ComplianceThreshold == nil {
		fields := map[string]string{}
		if in.WeeklyEmailEnabled == nil {
			fields["weeklyEmailEnabled"] = "Indica si el correo semanal está activo."
		}
		if in.ComplianceThreshold == nil {
			fields["complianceThreshold"] = "Indica el umbral de cumplimiento."
		}
		response.ValidationError(w, middleware.RequestIDFromContext(r.Context()), fields)
		return
	}
	s, err := h.service.UpdateSettings(r.Context(), user, org, domain.Settings{WeeklyEmailEnabled: *in.WeeklyEmailEnabled, ComplianceThreshold: *in.ComplianceThreshold})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, settingsDTO(s))
}

func (h *Handler) issueCertificate(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var in certificateRequest
	if !request.DecodeStrictJSON(w, r, &in, 1<<10) {
		return
	}
	out, err := h.service.IssueCertificate(r.Context(), user, org, in.From, in.To)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := 200
	if out.Created {
		status = 201
	}
	data(w, status, certificateOf(out.Certificate))
}

func (h *Handler) listCertificates(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	page, err := request.Pagination(r.URL.RawQuery, "pageSize", "pageToken")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out, err := h.service.ListCertificates(r.Context(), user, org, page)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]certificateDTO, 0, len(out.Items))
	for _, c := range out.Items {
		items = append(items, certificateOf(c))
	}
	response.JSON(w, 200, map[string]any{"data": items, "pagination": map[string]string{"nextPageToken": out.NextToken}})
}

// verify es público: devuelve solo datos agregados de la constancia y no revela identificadores.
func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		invalidRequest(w, r)
		return
	}
	c, err := h.service.VerifyCertificate(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	data(w, 200, publicCertificateDTO{true, c.OrganizationName, date(c.PeriodStart), date(c.PeriodEnd), c.AverageCompliance, c.Threshold, iso(c.IssuedAt), c.VerificationCode})
}

// utf8BOM es la marca de orden de bytes que antecede al CSV.
const utf8BOM = "\uFEFF"

// csvSink escribe el CSV en streaming; las cabeceras HTTP se fijan solo cuando la exportación procede.
type csvSink struct {
	w       http.ResponseWriter
	file    string
	out     *csv.Writer
	written int
}

func (s *csvSink) Begin() error {
	h := s.w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+s.file+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	s.w.WriteHeader(200)
	// La marca BOM de UTF-8 hace que Excel muestre bien las tildes; los clientes pueden ignorarla.
	if _, err := s.w.Write([]byte(utf8BOM)); err != nil {
		return err
	}
	s.out = csv.NewWriter(s.w)
	return s.out.Write(domain.ExportHeader)
}

func (s *csvSink) Row(v domain.ExportRow) error {
	if err := s.out.Write(v.Record()); err != nil {
		return err
	}
	s.written++
	if s.written%200 == 0 {
		s.out.Flush()
		if err := s.out.Error(); err != nil {
			return err
		}
		_ = http.NewResponseController(s.w).Flush()
	}
	return nil
}

func (s *csvSink) finish() error {
	if s.out == nil {
		return nil
	}
	s.out.Flush()
	return s.out.Error()
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	in, ok := h.period(w, r)
	if !ok {
		return
	}
	sink := &csvSink{w: w, file: "checklists_" + safeFilePart(in.From) + "_" + safeFilePart(in.To) + ".csv"}
	err := h.service.ExportRuns(r.Context(), user, org, in, sink)
	if sink.out == nil {
		// Nada se ha escrito todavía: se puede responder con el error uniforme.
		if err != nil {
			h.fail(w, r, err)
		}
		return
	}
	// Con la respuesta ya iniciada solo se puede cerrar el flujo; el cliente detecta el corte.
	if err == nil {
		err = sink.finish()
	}
	if err != nil && h.logger != nil {
		h.logger.Error("reports export interrupted", "requestId", middleware.RequestIDFromContext(r.Context()), "errorType", fmt.Sprintf("%T", err))
	}
}

// exportPDF entrega el mismo historial que el CSV como un documento PDF (resumen y tabla). Aplica la misma
// autorización, validación de período, alcance de sedes y auditoría; el PDF se arma completo antes de enviar el
// primer byte, así que cualquier error usa el JSON uniforme.
func (h *Handler) exportPDF(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	in, ok := h.period(w, r)
	if !ok {
		return
	}
	doc, err := h.service.ExportRunsDocument(r.Context(), user, org, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body, err := h.renderPDF(doc)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("reports pdf render failed", "requestId", middleware.RequestIDFromContext(r.Context()), "errorType", fmt.Sprintf("%T", err))
		}
		response.Error(w, 500, middleware.RequestIDFromContext(r.Context()), response.APIError{Code: "INTERNAL_ERROR", Message: "No se pudo generar el PDF. Inténtalo de nuevo."})
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/pdf")
	hd.Set("Content-Disposition", `attachment; filename="checklists_`+safeFilePart(in.From)+"_"+safeFilePart(in.To)+`.pdf"`)
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	if _, err = w.Write(body); err != nil && h.logger != nil {
		h.logger.Error("reports pdf interrupted", "requestId", middleware.RequestIDFromContext(r.Context()), "errorType", fmt.Sprintf("%T", err))
	}
}

// safeFilePart conserva solo dígitos y guiones para el nombre del archivo.
func safeFilePart(v string) string {
	b := make([]byte, 0, len(v))
	for i := 0; i < len(v) && i < 10; i++ {
		if c := v[i]; c >= '0' && c <= '9' || c == '-' {
			b = append(b, c)
		}
	}
	return string(b)
}
