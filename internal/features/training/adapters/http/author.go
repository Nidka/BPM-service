package traininghttp

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/go-chi/chi/v5"
	"strings"
)

func indexedField(name string, i int, field string) string {
	return fmt.Sprintf("%s[%d].%s", name, i, field)
}

// listCourses: GET /training/courses?status=...
func (h *Handler) listCourses(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || (len(query) == 1 && len(query["status"]) != 1) {
		invalidRequest(w, r)
		return
	}
	status := query.Get("status")
	items, err := h.service.ListCourses(r.Context(), user, org, status)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]summaryDTO, 0, len(items))
	for _, c := range items {
		out = append(out, summaryDTO{ID: c.ID, Code: c.Code, Title: c.Title, Status: c.Status, PublishedVersionNumber: c.PublishedVersionNumber, HasDraft: c.HasDraft})
	}
	data(w, http.StatusOK, map[string]any{"items": out})
}

// createCourse: POST /training/courses
func (h *Handler) createCourse(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body courseBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	created, err := h.service.CreateCourse(r.Context(), user, org, body.domain())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusCreated, map[string]any{"course": course(created, true)})
}

// getDraft: GET /training/courses/{courseID}/draft
func (h *Handler) getDraft(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	d, err := h.service.Draft(r.Context(), user, org, chi.URLParam(r, "courseID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, draft(d))
}

// putDraft: PUT /training/courses/{courseID}/draft
func (h *Handler) putDraft(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body draftBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	// Los campos ausentes llegan al dominio como valores inválidos (listas nil,
	// puntaje 0, respuesta -1) para que la autorización se evalúe antes que el formato.
	input := domain.DraftInput{VersionID: body.Version.ID, RowVersion: int(body.Version.RowVersion)}
	if body.Version.PassingScore != nil {
		input.PassingScore = *body.Version.PassingScore
	}
	if body.Lessons != nil {
		input.Lessons = make([]domain.Lesson, 0, len(*body.Lessons))
		for _, l := range *body.Lessons {
			lesson := domain.Lesson{ID: l.ID, Title: l.Title, Content: l.Content, DisplayOrder: l.DisplayOrder}
			if l.VideoMediaID != nil {
				lesson.VideoMediaID = *l.VideoMediaID
			}
			lesson.ExternalVideo = parseExternalVideo(l.ExternalVideo)
			input.Lessons = append(input.Lessons, lesson)
		}
	}
	if body.Questions != nil {
		input.Questions = make([]domain.Question, 0, len(*body.Questions))
		for _, q := range *body.Questions {
			question := domain.Question{ID: q.ID, Text: q.QuestionText, Options: q.Options, Explanation: q.Explanation, DisplayOrder: q.DisplayOrder, CorrectOption: -1}
			if q.LessonID != nil {
				question.LessonID = *q.LessonID
			}
			if q.CorrectOption != nil {
				question.CorrectOption = *q.CorrectOption
			}
			input.Questions = append(input.Questions, question)
		}
	}
	saved, err := h.service.SaveDraft(r.Context(), user, org, chi.URLParam(r, "courseID"), input)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, draft(saved))
}

// publish: POST /training/courses/{courseID}/publish
func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	out, err := h.service.Publish(r.Context(), user, org, chi.URLParam(r, "courseID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, publishDTO{Course: course(out.Course, true),
		Version: publishedVersionDTO{ID: out.Version.ID, VersionNumber: out.Version.Number, PassingScore: out.Version.PassingScore, PublishedAt: stampPtr(out.Version.PublishedAt)}})
}

// uploadHeaders conserva solo los encabezados que el cliente debe reenviar a R2.
// Host y Content-Length los calcula cada motor HTTP, también en navegador.
func uploadHeaders(in map[string][]string) map[string][]string {
	headers := map[string][]string{}
	for key, values := range in {
		switch strings.ToLower(key) {
		case "content-type", "if-none-match", "x-amz-meta-sha256", "cache-control", "content-disposition":
			headers[key] = values
		}
	}
	return headers
}

// reserveVideo: POST /training/videos/uploads
func (h *Handler) reserveVideo(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body videoUploadBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	reservation, err := h.service.ReserveVideo(r.Context(), user, org, domain.VideoUploadInput{ID: body.ID, OriginalName: body.OriginalName, MIMEType: body.MIMEType, SizeBytes: body.SizeBytes, SHA256: body.SHA256})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := reservationDTO{File: videoFile(reservation.File)}
	if reservation.Upload != nil {
		out.Upload = uploadTicket(*reservation.Upload)
	}
	noStore(w)
	data(w, http.StatusCreated, out)
}

func uploadTicket(t contracts.SignedRequest) *uploadDTO {
	return &uploadDTO{Method: http.MethodPut, URL: t.URL, Headers: uploadHeaders(t.Headers), ExpiresAt: stamp(t.ExpiresAt)}
}

// confirmVideo: POST /training/videos/{mediaID}/confirm
func (h *Handler) confirmVideo(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	file, err := h.service.ConfirmVideo(r.Context(), user, org, chi.URLParam(r, "mediaID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, map[string]any{"file": videoFile(file)})
}
