package traininghttp

import (
	"net/http"

	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/catalog"
	"github.com/go-chi/chi/v5"
)

type questionBankDTO struct {
	Version         int `json:"version"`
	ItemsPerAttempt int `json:"itemsPerAttempt"`
	PoolSize        int `json:"poolSize"`
}

type reviewDTO struct {
	DueCount    int      `json:"dueCount"`
	QuestionIDs []string `json:"questionIds"`
}

// sourceDTO es una fuente oficial del banco: código (P01), etiqueta corta, título completo y enlace.
type sourceDTO struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func sources(codes []string) []sourceDTO {
	if len(codes) == 0 {
		return nil
	}
	out := make([]sourceDTO, 0, len(codes))
	for _, s := range application.SourceLabels(codes) {
		out = append(out, sourceDTO{Code: s.Code, Label: s.Short, Title: s.Title, URL: s.URL})
	}
	return out
}

type evaluationBody struct {
	ID          string `json:"id"`
	AttemptType string `json:"attemptType"`
}

type evaluationDTO struct {
	ID            string             `json:"id"`
	VersionID     string             `json:"versionId"`
	AttemptType   string             `json:"attemptType"`
	BankVersion   int                `json:"bankVersion"`
	AttemptNumber int                `json:"attemptNumber"`
	PassingScore  int                `json:"passingScore"`
	CreatedAt     string             `json:"createdAt"`
	Questions     []learnQuestionDTO `json:"questions"`
}

// startEvaluation: POST /training/courses/{courseID}/evaluations
func (h *Handler) startEvaluation(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok {
		return
	}
	var body evaluationBody
	if !request.DecodeStrictJSON(w, r, &body, maxBody) {
		return
	}
	out, err := h.service.StartEvaluation(r.Context(), user, org, chi.URLParam(r, "courseID"), application.EvaluationInput{ID: body.ID, AttemptType: body.AttemptType})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	dto := evaluationDTO{ID: out.ID, VersionID: out.VersionID, AttemptType: out.AttemptType, BankVersion: out.BankVersion, AttemptNumber: out.AttemptNumber,
		PassingScore: out.PassingScore, CreatedAt: stamp(out.CreatedAt), Questions: make([]learnQuestionDTO, 0, len(out.Questions))}
	for _, q := range out.Questions {
		dto.Questions = append(dto.Questions, learnQuestionDTO{ID: q.ID, LessonID: nullable(q.LessonID), QuestionText: q.Text, Options: q.Options, DisplayOrder: q.DisplayOrder})
	}
	noStore(w)
	status := http.StatusCreated
	if out.Replayed {
		status = http.StatusOK
	}
	data(w, status, map[string]any{"evaluation": dto})
}

type bankItemDTO struct {
	Code          string      `json:"code"`
	Bloom         string      `json:"bloom"`
	QuestionText  string      `json:"questionText"`
	Options       []string    `json:"options"`
	CorrectOption int         `json:"correctOption"`
	Key           string      `json:"key"`
	Justification string      `json:"justification"`
	Sources       []sourceDTO `json:"sources"`
	Recommended   bool        `json:"recommended"`
	LessonTitle   string      `json:"lessonTitle"`
}

type bankDTO struct {
	Course      courseDTO     `json:"course"`
	Module      string        `json:"module"`
	BankVersion int           `json:"bankVersion"`
	PassScore   int           `json:"passingScore"`
	Items       []bankItemDTO `json:"items"`
}

// bank: GET /training/courses/{courseID}/bank (solo administración, solo lectura)
func (h *Handler) bank(w http.ResponseWriter, r *http.Request) {
	user, org, ok := h.identity(w, r)
	if !ok || !emptyRequest(w, r) {
		return
	}
	view, err := h.service.Bank(r.Context(), user, org, chi.URLParam(r, "courseID"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	dto := bankDTO{Course: course(view.Course, false), Module: view.Module, PassScore: view.Version.PassingScore, Items: make([]bankItemDTO, 0, len(view.Items))}
	for _, item := range view.Items {
		q := item.Question
		if q.BankVersion > dto.BankVersion {
			dto.BankVersion = q.BankVersion
		}
		dto.Items = append(dto.Items, bankItemDTO{Code: q.ItemCode, Bloom: q.Bloom, QuestionText: q.Text, Options: q.Options, CorrectOption: q.CorrectOption,
			Key: string(catalog.OptionLetters[q.CorrectOption]), Justification: q.Explanation, Sources: sources(q.Sources), Recommended: item.Recommended, LessonTitle: item.LessonTitle})
	}
	noStore(w)
	data(w, http.StatusOK, map[string]any{"bank": dto})
}
