package traininghttp

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// DTO de entrada. Son tipos distintos de las entidades de dominio y de las filas SQL.

type courseBody struct {
	Code                     string `json:"code"`
	Title                    string `json:"title"`
	Description              string `json:"description"`
	EstimatedDurationMinutes *int   `json:"estimatedDurationMinutes"`
}

func (b courseBody) domain() domain.NewCourse {
	return domain.NewCourse{Code: b.Code, Title: b.Title, Description: b.Description, EstimatedDurationMinutes: b.EstimatedDurationMinutes}
}

type progressBody struct {
	LastLessonID string `json:"lastLessonId"`
}

type studyBody struct {
	QuestionID     string `json:"questionId"`
	SelectedOption *int   `json:"selectedOption"`
}

type answerBody struct {
	QuestionID     string `json:"questionId"`
	SelectedOption *int   `json:"selectedOption"`
}

type attemptBody struct {
	ID                string       `json:"id"`
	ClientOperationID string       `json:"clientOperationId"`
	VersionID         string       `json:"versionId"`
	AttemptType       string       `json:"attemptType"`
	LessonID          *string      `json:"lessonId"`
	StartedAt         time.Time    `json:"startedAt"`
	ClientCreatedAt   time.Time    `json:"clientCreatedAt"`
	Answers           []answerBody `json:"answers"`
}

// rowVersion acepta el valor como texto ("3") o número (3).
type rowVersion int

func (v *rowVersion) UnmarshalJSON(raw []byte) error {
	raw = bytes.Trim(bytes.TrimSpace(raw), `"`)
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return err
	}
	*v = rowVersion(n)
	return nil
}

type draftVersionBody struct {
	ID            string     `json:"id"`
	VersionNumber int        `json:"versionNumber"`
	PassingScore  *int       `json:"passingScore"`
	RowVersion    rowVersion `json:"rowVersion"`
}

type draftLessonBody struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Content      string  `json:"content"`
	DisplayOrder int     `json:"displayOrder"`
	VideoMediaID *string `json:"videoMediaId"`
	// ExternalVideo acepta una URL de YouTube (texto) o un objeto {provider, videoId | url}.
	ExternalVideo json.RawMessage `json:"externalVideo"`
}

type draftQuestionBody struct {
	ID            string   `json:"id"`
	LessonID      *string  `json:"lessonId"`
	QuestionText  string   `json:"questionText"`
	Options       []string `json:"options"`
	CorrectOption *int     `json:"correctOption"`
	Explanation   string   `json:"explanation"`
	DisplayOrder  int      `json:"displayOrder"`
}

type draftBody struct {
	Version   draftVersionBody     `json:"version"`
	Lessons   *[]draftLessonBody   `json:"lessons"`
	Questions *[]draftQuestionBody `json:"questions"`
}

type videoUploadBody struct {
	ID           string `json:"id"`
	OriginalName string `json:"originalName"`
	MIMEType     string `json:"mimeType"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
}

// DTO de salida.

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func stampPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := stamp(*t)
	return &s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type courseDTO struct {
	ID                       string  `json:"id"`
	Code                     string  `json:"code"`
	Title                    string  `json:"title"`
	Description              string  `json:"description"`
	EstimatedDurationMinutes *int    `json:"estimatedDurationMinutes"`
	Status                   *string `json:"status,omitempty"`
}

func course(c domain.Course, withStatus bool) courseDTO {
	out := courseDTO{ID: c.ID, Code: c.Code, Title: c.Title, Description: c.Description, EstimatedDurationMinutes: c.EstimatedDurationMinutes}
	if withStatus {
		status := c.Status
		out.Status = &status
	}
	return out
}

type progressDTO struct {
	Status           string  `json:"status"`
	LastLessonID     *string `json:"lastLessonId"`
	BestScore        *int    `json:"bestScore"`
	ExperiencePoints int     `json:"experiencePoints"`
	CompletedAt      *string `json:"completedAt"`
}

func progress(p domain.Progress) progressDTO {
	status := p.Status
	if status == "" {
		status = domain.ProgressNotStarted
	}
	return progressDTO{Status: status, LastLessonID: nullable(p.LastLessonID), BestScore: p.BestScore, ExperiencePoints: p.ExperiencePoints, CompletedAt: stampPtr(p.CompletedAt)}
}

type certificateDTO struct {
	ID               string `json:"id"`
	VerificationCode string `json:"verificationCode"`
	IssuedAt         string `json:"issuedAt"`
}

func certificate(c *domain.Certificate) *certificateDTO {
	if c == nil {
		return nil
	}
	return &certificateDTO{ID: c.ID, VerificationCode: c.VerificationCode, IssuedAt: stamp(c.IssuedAt)}
}

type learnVersionDTO struct {
	ID            string `json:"id"`
	VersionNumber int    `json:"versionNumber"`
	PassingScore  int    `json:"passingScore"`
}

type videoDTO struct {
	MediaID   string `json:"mediaId"`
	MIMEType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
}

type learnLessonDTO struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	DisplayOrder int       `json:"displayOrder"`
	Video        *videoDTO `json:"video"`
	// ExternalVideo: el cliente arma https://www.youtube-nocookie.com/embed/{videoId}.
	ExternalVideo *externalVideoDTO `json:"externalVideo"`
}

// learnQuestionDTO no tiene campos para la respuesta correcta ni la explicación,
// de modo que no pueden serializarse por error.
type learnQuestionDTO struct {
	ID           string   `json:"id"`
	LessonID     *string  `json:"lessonId"`
	QuestionText string   `json:"questionText"`
	Options      []string `json:"options"`
	DisplayOrder int      `json:"displayOrder"`
}

type courseViewDTO struct {
	Course      courseDTO          `json:"course"`
	Version     learnVersionDTO    `json:"version"`
	Lessons     []learnLessonDTO   `json:"lessons"`
	Questions   []learnQuestionDTO `json:"questions"`
	Progress    progressDTO        `json:"progress"`
	Certificate *certificateDTO    `json:"certificate"`
}

func courseView(v domain.CourseView) courseViewDTO {
	out := courseViewDTO{Course: course(v.Course, false), Version: learnVersionDTO{ID: v.Version.ID, VersionNumber: v.Version.Number, PassingScore: v.Version.PassingScore},
		Lessons: make([]learnLessonDTO, 0, len(v.Lessons)), Questions: make([]learnQuestionDTO, 0, len(v.Questions)),
		Progress: progress(v.Progress), Certificate: certificate(v.Certificate)}
	for _, l := range v.Lessons {
		item := learnLessonDTO{ID: l.ID, Title: l.Title, Content: l.Content, DisplayOrder: l.DisplayOrder}
		if l.Video != nil {
			item.Video = &videoDTO{MediaID: l.Video.MediaID, MIMEType: l.Video.MIMEType, SizeBytes: l.Video.SizeBytes}
		}
		item.ExternalVideo = externalVideo(l.ExternalVideo)
		out.Lessons = append(out.Lessons, item)
	}
	for _, q := range v.Questions {
		out.Questions = append(out.Questions, learnQuestionDTO{ID: q.ID, LessonID: nullable(q.LessonID), QuestionText: q.Text, Options: q.Options, DisplayOrder: q.DisplayOrder})
	}
	return out
}

type videoAccessDTO struct {
	URL       string `json:"url"`
	MIMEType  string `json:"mimeType"`
	ExpiresAt string `json:"expiresAt"`
}

func videoAccess(a application.VideoAccess) videoAccessDTO {
	return videoAccessDTO{URL: a.URL, MIMEType: a.MIMEType, ExpiresAt: stamp(a.ExpiresAt)}
}

type studyDTO struct {
	Correct       bool   `json:"correct"`
	CorrectOption int    `json:"correctOption"`
	Explanation   string `json:"explanation"`
}

type attemptDTO struct {
	ID          string `json:"id"`
	AttemptType string `json:"attemptType"`
	Score       int    `json:"score"`
	Passed      bool   `json:"passed"`
	CompletedAt string `json:"completedAt"`
}

type resultDTO struct {
	QuestionID     string `json:"questionId"`
	SelectedOption int    `json:"selectedOption"`
	Correct        bool   `json:"correct"`
	CorrectOption  int    `json:"correctOption"`
	Explanation    string `json:"explanation"`
}

type outcomeDTO struct {
	Attempt     attemptDTO      `json:"attempt"`
	Results     []resultDTO     `json:"results"`
	Progress    progressDTO     `json:"progress"`
	Certificate *certificateDTO `json:"certificate"`
	// Achievements son las insignias otorgadas por este intento.
	Achievements []achievementDTO `json:"achievements"`
}

func outcome(o application.AttemptOutcome) outcomeDTO {
	out := outcomeDTO{Attempt: attemptDTO{ID: o.ID, AttemptType: o.AttemptType, Score: o.Score, Passed: o.Passed, CompletedAt: stamp(o.CompletedAt)},
		Results: make([]resultDTO, 0, len(o.Results)), Progress: progress(o.Progress), Certificate: certificate(o.Certificate), Achievements: achievementList(o.Achievements)}
	for _, r := range o.Results {
		out.Results = append(out.Results, resultDTO{QuestionID: r.QuestionID, SelectedOption: r.SelectedOption, Correct: r.Correct, CorrectOption: r.CorrectOption, Explanation: r.Explanation})
	}
	return out
}

type summaryDTO struct {
	ID                     string `json:"id"`
	Code                   string `json:"code"`
	Title                  string `json:"title"`
	Status                 string `json:"status"`
	PublishedVersionNumber *int   `json:"publishedVersionNumber"`
	HasDraft               bool   `json:"hasDraft"`
}

type draftVersionDTO struct {
	ID            string `json:"id"`
	VersionNumber int    `json:"versionNumber"`
	PassingScore  int    `json:"passingScore"`
	RowVersion    string `json:"rowVersion"`
}

type draftLessonDTO struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Content       string            `json:"content"`
	DisplayOrder  int               `json:"displayOrder"`
	VideoMediaID  *string           `json:"videoMediaId"`
	ExternalVideo *externalVideoDTO `json:"externalVideo"`
}

type draftQuestionDTO struct {
	ID            string   `json:"id"`
	LessonID      *string  `json:"lessonId"`
	QuestionText  string   `json:"questionText"`
	Options       []string `json:"options"`
	CorrectOption int      `json:"correctOption"`
	Explanation   string   `json:"explanation"`
	DisplayOrder  int      `json:"displayOrder"`
}

type draftDTO struct {
	Course    courseDTO          `json:"course"`
	Version   draftVersionDTO    `json:"version"`
	Lessons   []draftLessonDTO   `json:"lessons"`
	Questions []draftQuestionDTO `json:"questions"`
}

func draft(d domain.Draft) draftDTO {
	out := draftDTO{Course: course(d.Course, true),
		Version: draftVersionDTO{ID: d.Version.ID, VersionNumber: d.Version.Number, PassingScore: d.Version.PassingScore, RowVersion: strconv.Itoa(d.Version.RowVersion)},
		Lessons: make([]draftLessonDTO, 0, len(d.Lessons)), Questions: make([]draftQuestionDTO, 0, len(d.Questions))}
	for _, l := range d.Lessons {
		out.Lessons = append(out.Lessons, draftLessonDTO{ID: l.ID, Title: l.Title, Content: l.Content, DisplayOrder: l.DisplayOrder, VideoMediaID: nullable(l.VideoMediaID), ExternalVideo: externalVideo(l.ExternalVideo)})
	}
	for _, q := range d.Questions {
		out.Questions = append(out.Questions, draftQuestionDTO{ID: q.ID, LessonID: nullable(q.LessonID), QuestionText: q.Text, Options: q.Options, CorrectOption: q.CorrectOption, Explanation: q.Explanation, DisplayOrder: q.DisplayOrder})
	}
	return out
}

type publishedVersionDTO struct {
	ID            string  `json:"id"`
	VersionNumber int     `json:"versionNumber"`
	PassingScore  int     `json:"passingScore"`
	PublishedAt   *string `json:"publishedAt"`
}

type publishDTO struct {
	Course  courseDTO           `json:"course"`
	Version publishedVersionDTO `json:"version"`
}

type videoFileDTO struct {
	ID           string  `json:"id"`
	OriginalName string  `json:"originalName"`
	MIMEType     string  `json:"mimeType"`
	SizeBytes    int64   `json:"sizeBytes"`
	SHA256       string  `json:"sha256"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"createdAt"`
	UploadedAt   *string `json:"uploadedAt"`
}

func videoFile(f domain.VideoFile) videoFileDTO {
	return videoFileDTO{ID: f.ID, OriginalName: f.OriginalName, MIMEType: f.MIMEType, SizeBytes: f.SizeBytes, SHA256: f.SHA256, Status: f.Status, CreatedAt: stamp(f.CreatedAt), UploadedAt: stampPtr(f.UploadedAt)}
}

type uploadDTO struct {
	Method    string              `json:"method"`
	URL       string              `json:"url"`
	Headers   map[string][]string `json:"headers"`
	ExpiresAt string              `json:"expiresAt"`
}

type reservationDTO struct {
	File   videoFileDTO `json:"file"`
	Upload *uploadDTO   `json:"upload"`
}
