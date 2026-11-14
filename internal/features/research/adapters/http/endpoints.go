package researchhttp

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/research/application"
	"github.com/Nidka/BPM-service/internal/features/research/domain"
	"github.com/go-chi/chi/v5"
)

// ---- Participante ----

func (h *Handler) participation(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok || !emptyQuery(w, r) {
		return
	}
	out, err := h.service.Participation(r.Context(), user, chi.URLParam(r, "organizationID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, participationDTO(out))
}

func (h *Handler) consent(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	var body consentBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	f := map[string]string{}
	if body.Accepted == nil {
		f["accepted"] = "Indica si aceptas o revocas."
	}
	if !domain.ValidConsentVersion(body.DocumentVersion) {
		f["documentVersion"] = "Indica la versión del documento."
	}
	if len(f) > 0 {
		h.fail(w, r, &domain.ValidationError{Fields: f})
		return
	}
	out, err := h.service.RecordConsent(r.Context(), user, chi.URLParam(r, "organizationID"), body.DocumentVersion, *body.Accepted)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, participationDTO(out))
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	var body submissionBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	in := domain.SubmissionInput{ID: body.ID, ClientOperationID: body.ClientOperationID, VersionID: body.InstrumentVersionID, Moment: body.Moment,
		ClientCreatedAt: body.ClientCreatedAt, Answers: make([]domain.AnswerInput, 0, len(body.Answers))}
	fields := map[string]string{}
	for i, a := range body.Answers {
		if a.Value == nil {
			fields[fmt.Sprintf("answers[%d].value", i)] = "Responde la pregunta."
			continue
		}
		in.Answers = append(in.Answers, domain.AnswerInput{QuestionID: a.QuestionID, Value: *a.Value})
	}
	if len(fields) > 0 {
		h.fail(w, r, &domain.AnswersError{Fields: fields})
		return
	}
	out, err := h.service.SubmitSurvey(r.Context(), user, chi.URLParam(r, "organizationID"), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	data(w, status, map[string]any{"id": out.ID, "moment": out.Moment, "instrumentVersionId": out.VersionID, "submittedAt": ts(out.SubmittedAt), "scores": scoresDTO(out.Scores)})
}

// ---- Acceso y concesiones ----

func (h *Handler) access(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok || !emptyQuery(w, r) {
		return
	}
	research, grants, err := h.service.Access(r.Context(), user)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, map[string]any{"canResearch": research, "canManageGrants": grants})
}

func (h *Handler) listGrants(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	_, p, ok := h.query(w, r)
	if !ok {
		return
	}
	page, err := h.service.ListGrants(r.Context(), user, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, g := range page.Items {
		items = append(items, grantDTO(g))
	}
	dataPage(w, page, items)
}

func (h *Handler) grant(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	var body grantBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	g, err := h.service.GrantAccess(r.Context(), user, body.Email)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, grantDTO(g))
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok || !emptyQuery(w, r) {
		return
	}
	if err := h.service.RevokeAccess(r.Context(), user, chi.URLParam(r, "userID")); err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// ---- Consentimiento (investigador) ----

func (h *Handler) listConsentDocuments(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	_, p, ok := h.query(w, r)
	if !ok {
		return
	}
	page, err := h.service.ListConsentDocuments(r.Context(), user, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, d := range page.Items {
		items = append(items, consentDocumentDTO(d))
	}
	dataPage(w, page, items)
}

func (h *Handler) publishConsentDocument(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	var body consentDocumentBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	d, err := h.service.PublishConsentDocument(r.Context(), user, domain.ConsentDocumentInput{Version: body.Version, Title: body.Title, Text: body.Text, ResponsibleInvestigator: body.ResponsibleInvestigator})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 201, consentDocumentDTO(d))
}

// ---- Instrumentos ----

func (h *Handler) listInstruments(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	_, p, ok := h.query(w, r)
	if !ok {
		return
	}
	page, err := h.service.ListInstruments(r.Context(), user, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, i := range page.Items {
		m := instrumentDTO(i.Instrument)
		m["publishedVersionNumber"], m["hasDraft"] = i.PublishedVersion, i.HasDraft
		items = append(items, m)
	}
	dataPage(w, page, items)
}

func (h *Handler) createInstrument(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	var body instrumentBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	h.created(w, r, user, body)
}

func (h *Handler) importInstrument(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	var body importBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	h.created(w, r, user, body.instrumentBody)
}

func (h *Handler) created(w http.ResponseWriter, r *http.Request, user string, body instrumentBody) {
	d, err := h.service.CreateInstrument(r.Context(), user, domain.InstrumentInput{Code: body.Code, Name: body.Name, Description: body.Description, IsCAP: body.IsCAP}, questionInputs(body.Questions))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 201, draftDTO(d))
}

func (h *Handler) getInstrument(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok || !emptyQuery(w, r) {
		return
	}
	d, err := h.service.GetInstrument(r.Context(), user, chi.URLParam(r, "instrumentID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	versions := make([]map[string]any, 0, len(d.Versions))
	for _, v := range d.Versions {
		versions = append(versions, versionDTO(v))
	}
	data(w, 200, map[string]any{"instrument": instrumentDTO(d.Instrument), "versions": versions})
}

func (h *Handler) getDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok || !emptyQuery(w, r) {
		return
	}
	d, err := h.service.GetDraft(r.Context(), user, chi.URLParam(r, "instrumentID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, draftDTO(d))
}

func (h *Handler) createDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok || !emptyQuery(w, r) {
		return
	}
	d, err := h.service.CreateDraft(r.Context(), user, chi.URLParam(r, "instrumentID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 201, draftDTO(d))
}

func (h *Handler) putDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	var body draftBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	if body.RowVersion == nil || body.Questions == nil {
		h.fail(w, r, &domain.ValidationError{Fields: map[string]string{"rowVersion": "Envía rowVersion y la lista completa de preguntas."}})
		return
	}
	d, err := h.service.ReplaceDraft(r.Context(), user, chi.URLParam(r, "instrumentID"), int(*body.RowVersion), questionInputs(body.Questions))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, draftDTO(d))
}

func (h *Handler) publishDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok || !emptyQuery(w, r) {
		return
	}
	v, err := h.service.PublishDraft(r.Context(), user, chi.URLParam(r, "instrumentID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 200, map[string]any{"version": versionDTO(v)})
}

// ---- Resultados, empresas y exportación ----

func (h *Handler) listOrganizations(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	_, p, ok := h.query(w, r)
	if !ok {
		return
	}
	page, err := h.service.ListOrganizations(r.Context(), user, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, o := range page.Items {
		items = append(items, map[string]any{"id": o.ID, "name": o.Name})
	}
	dataPage(w, page, items)
}

func (h *Handler) results(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	f, p, ok := h.query(w, r, "instrumentVersionId")
	if !ok {
		return
	}
	v, page, err := h.service.Results(r.Context(), user, f["instrumentVersionId"], p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, resultDTO(it))
	}
	w.Header().Set("Cache-Control", "no-store")
	respondResults(w, v, items, page.NextToken)
}

func (h *Handler) exportCSV(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	f, rest, ok := h.query(w, r, "instrumentVersionId", "organizationId")
	if !ok {
		return
	}
	_ = rest
	out, err := h.service.Export(r.Context(), user, f["instrumentVersionId"], f["organizationId"])
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cap-v%d-respuestas.csv"`, out.VersionNumber))
	w.Header().Set("Cache-Control", "no-store")
	writeCSV(w, out)
}

// writeCSV escribe la exportación anonimizada: código seudónimo, empresa, momento,
// fecha, versión, respuestas (una columna por pregunta) y puntajes. Nunca incluye
// correo, UID, nombre ni identificadores de usuario.
func writeCSV(w http.ResponseWriter, d application.ExportData) {
	cw := csv.NewWriter(w)
	header := []string{"participantCode", "organizationId", "moment", "submittedDate", "instrumentVersion"}
	prefix := map[string]string{domain.SectionKnowledge: "k", domain.SectionAttitude: "a", domain.SectionPractice: "p"}
	counters := map[string]int{}
	for _, q := range d.Questions {
		counters[q.Section]++
		header = append(header, prefix[q.Section]+strconv.Itoa(counters[q.Section]))
	}
	header = append(header, "knowledgeCorrect", "knowledgeTotal", "knowledgePercent", "attitudeMean", "practiceMean")
	_ = cw.Write(header)
	for _, row := range d.Rows {
		rec := []string{safeCell(row.ParticipantCode), safeCell(row.OrganizationID), row.Moment, row.SubmittedAt.UTC().Format("2006-01-02"), strconv.Itoa(d.VersionNumber)}
		for i := range d.Questions {
			v := ""
			if i < len(row.Answers) {
				v = strconv.Itoa(row.Answers[i])
			}
			rec = append(rec, v)
		}
		s := row.Scores
		rec = append(rec, strconv.Itoa(s.KnowledgeCorrect), strconv.Itoa(s.KnowledgeTotal),
			strconv.FormatFloat(s.Knowledge, 'f', 2, 64), strconv.FormatFloat(s.Attitude, 'f', 2, 64), strconv.FormatFloat(s.Practice, 'f', 2, 64))
		_ = cw.Write(rec)
	}
	cw.Flush()
}

// safeCell neutraliza celdas que una hoja de cálculo interpretaría como fórmula.
func safeCell(v string) string {
	if v != "" && strings.ContainsAny(v[:1], "=+-@\t\r") {
		return "'" + v
	}
	return v
}

// ---- Necesidades (RF-50) ----

func (h *Handler) addNeed(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	var body needBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	in := domain.NeedInput{OrganizationID: body.OrganizationID, Category: body.Category, Description: body.Description}
	if body.DetectedAt != nil {
		in.DetectedAt = *body.DetectedAt
	}
	n, err := h.service.AddNeed(r.Context(), user, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, 201, needDTO(n))
}

func (h *Handler) listNeeds(w http.ResponseWriter, r *http.Request) {
	user, ok := uid(w, r)
	if !ok {
		return
	}
	f, p, ok := h.query(w, r, "organizationId")
	if !ok {
		return
	}
	page, err := h.service.ListNeeds(r.Context(), user, f["organizationId"], p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, n := range page.Items {
		items = append(items, needDTO(n))
	}
	dataPage(w, page, items)
}

// respondResults responde los resultados consolidados con la versión evaluada.
func respondResults(w http.ResponseWriter, v domain.Version, items []map[string]any, next string) {
	response.JSON(w, 200, map[string]any{
		"data":       items,
		"version":    map[string]any{"id": v.ID, "versionNumber": v.Number},
		"pagination": map[string]string{"nextPageToken": next},
	})
}
