package traininghttp

import (
	"net/http"

	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/go-chi/chi/v5"
)

// course: GET /training/courses/{courseID}
func (h *Handler) course(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	view, err := h.service.Course(r.Context(), user, org, chi.URLParam(r, "courseID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, courseView(view))
}

// lessonVideo: POST /training/courses/{courseID}/lessons/{lessonID}/video
func (h *Handler) lessonVideo(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	access, err := h.service.LessonVideo(r.Context(), user, org, chi.URLParam(r, "courseID"), chi.URLParam(r, "lessonID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	noStore(w)
	data(w, http.StatusOK, videoAccess(access))
}

// saveProgress: PUT /training/courses/{courseID}/progress
func (h *Handler) saveProgress(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body progressBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	saved, err := h.service.SaveProgress(r.Context(), user, org, chi.URLParam(r, "courseID"), body.LastLessonID, body.CompletedLessonID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, map[string]any{"progress": progress(saved)})
}

// study: POST /training/courses/{courseID}/study/answers (sin persistencia)
func (h *Handler) study(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body studyBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	if body.SelectedOption == nil {
		h.fail(w, r, &domain.ValidationError{Fields: map[string]string{"selectedOption": "Elige una opción."}})
		return
	}
	feedback, err := h.service.StudyAnswer(r.Context(), user, org, chi.URLParam(r, "courseID"), body.QuestionID, *body.SelectedOption)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data(w, http.StatusOK, studyDTO{Correct: feedback.Correct, CorrectOption: feedback.CorrectOption, Explanation: feedback.Explanation,
		ItemCode: feedback.ItemCode, Bloom: feedback.Bloom, Sources: sources(feedback.Sources), XPEarned: feedback.XP})
}

// attempt: POST /training/courses/{courseID}/attempts
func (h *Handler) attempt(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body attemptBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	input := domain.AttemptInput{ID: body.ID, ClientOperationID: body.ClientOperationID, VersionID: body.VersionID, AttemptType: body.AttemptType,
		StartedAt: body.StartedAt, ClientCreatedAt: body.ClientCreatedAt, Answers: make([]domain.Answer, 0, len(body.Answers))}
	if body.LessonID != nil {
		input.LessonID = *body.LessonID
	}
	fields := map[string]string{}
	for i, a := range body.Answers {
		if a.SelectedOption == nil {
			fields[indexedField("answers", i, "selectedOption")] = "Elige una opción."
			continue
		}
		input.Answers = append(input.Answers, domain.Answer{QuestionID: a.QuestionID, SelectedOption: *a.SelectedOption})
	}
	if len(fields) > 0 {
		h.fail(w, r, &domain.AnswersError{Fields: fields})
		return
	}
	out, err := h.service.SubmitAttempt(r.Context(), user, org, chi.URLParam(r, "courseID"), input)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	data(w, status, outcome(out))
}
