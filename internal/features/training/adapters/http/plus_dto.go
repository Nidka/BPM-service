package traininghttp

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// ---- Video externo ----

type externalVideoDTO struct {
	Provider string `json:"provider"`
	VideoID  string `json:"videoId"`
}

func externalVideo(v *domain.ExternalVideo) *externalVideoDTO {
	if v == nil {
		return nil
	}
	return &externalVideoDTO{Provider: v.Provider, VideoID: v.VideoID}
}

// externalVideoBody es la forma de objeto: exactamente uno de videoId o url.
type externalVideoBody struct {
	Provider string `json:"provider"`
	VideoID  string `json:"videoId"`
	URL      string `json:"url"`
}

// parseExternalVideo normaliza la entrada de autoría a un video de YouTube. Un
// valor ausente o null no es video externo. Cualquier valor inválido (otro
// dominio, formato roto, tipo inesperado) se traduce a un marcador inválido que
// el dominio rechaza con 422 después de autorizar; nunca se conserva una URL arbitraria.
func parseExternalVideo(raw json.RawMessage) *domain.ExternalVideo {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	invalidMarker := &domain.ExternalVideo{}
	var text string
	if raw[0] == '"' {
		if json.Unmarshal(raw, &text) != nil {
			return invalidMarker
		}
		if v, ok := domain.NormalizeExternalVideo(domain.ProviderYouTube, "", text); ok {
			return v
		}
		return invalidMarker
	}
	var body externalVideoBody
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		return invalidMarker
	}
	if v, ok := domain.NormalizeExternalVideo(body.Provider, body.VideoID, body.URL); ok {
		return v
	}
	return invalidMarker
}

// ---- Asignaciones ----

type assignmentTargetBody struct {
	Type       string `json:"type"`
	UserID     string `json:"userId"`
	Role       string `json:"role"`
	LocationID string `json:"locationId"`
}

type assignmentBody struct {
	ID       string               `json:"id"`
	CourseID string               `json:"courseId"`
	Target   assignmentTargetBody `json:"target"`
	DueAt    *time.Time           `json:"dueAt"`
}

func (b assignmentBody) domain() domain.NewAssignment {
	return domain.NewAssignment{ID: b.ID, CourseID: b.CourseID, DueAt: b.DueAt,
		Target: domain.AssignmentTarget{Type: b.Target.Type, UserID: b.Target.UserID, Role: b.Target.Role, LocationID: b.Target.LocationID}}
}

type assignmentCourseDTO struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Title string `json:"title"`
}

type assignmentTargetDTO struct {
	Type         string  `json:"type"`
	UserID       *string `json:"userId"`
	UserName     *string `json:"userName"`
	Role         *string `json:"role"`
	LocationID   *string `json:"locationId"`
	LocationName *string `json:"locationName"`
}

type assignmentDTO struct {
	ID          string              `json:"id"`
	Course      assignmentCourseDTO `json:"course"`
	Target      assignmentTargetDTO `json:"target"`
	DueAt       *string             `json:"dueAt"`
	Status      string              `json:"status"`
	CreatedAt   string              `json:"createdAt"`
	CancelledAt *string             `json:"cancelledAt"`
}

func assignment(a domain.Assignment) assignmentDTO {
	return assignmentDTO{ID: a.ID, Course: assignmentCourseDTO{ID: a.CourseID, Code: a.CourseCode, Title: a.CourseTitle},
		Target: assignmentTargetDTO{Type: a.Target.Type, UserID: nullable(a.Target.UserID), UserName: nullable(a.UserName), Role: nullable(a.Target.Role),
			LocationID: nullable(a.Target.LocationID), LocationName: nullable(a.LocationName)},
		DueAt: stampPtr(a.DueAt), Status: a.Status, CreatedAt: stamp(a.CreatedAt), CancelledAt: stampPtr(a.CancelledAt)}
}

type myAssignmentDTO struct {
	AssignmentID   string    `json:"assignmentId"`
	Course         courseDTO `json:"course"`
	DueAt          *string   `json:"dueAt"`
	AssignedAt     string    `json:"assignedAt"`
	Status         string    `json:"status"`
	ProgressStatus string    `json:"progressStatus"`
	BestScore      *int      `json:"bestScore"`
	CompletedAt    *string   `json:"completedAt"`
}

func myAssignment(m domain.MyAssignment) myAssignmentDTO {
	return myAssignmentDTO{AssignmentID: m.AssignmentID, Course: course(m.Course, false), DueAt: stampPtr(m.DueAt), AssignedAt: stamp(m.AssignedAt),
		Status: m.Status, ProgressStatus: m.ProgressStatus, BestScore: m.BestScore, CompletedAt: stampPtr(m.CompletedAt)}
}

// ---- Insignias ----

type achievementDTO struct {
	ID                 string  `json:"id"`
	Code               string  `json:"code"`
	Name               string  `json:"name"`
	Description        string  `json:"description"`
	Points             int     `json:"points"`
	CriterionType      string  `json:"criterionType"`
	CriterionThreshold int     `json:"criterionThreshold"`
	Active             bool    `json:"active"`
	AwardedAt          *string `json:"awardedAt"`
}

func achievement(a domain.Achievement) achievementDTO {
	return achievementDTO{ID: a.ID, Code: a.Code, Name: a.Name, Description: a.Description, Points: a.Points, CriterionType: a.Criterion,
		CriterionThreshold: a.Threshold, Active: a.Active, AwardedAt: stampPtr(a.AwardedAt)}
}

func achievementList(items []domain.Achievement) []achievementDTO {
	out := make([]achievementDTO, 0, len(items))
	for _, a := range items {
		out = append(out, achievement(a))
	}
	return out
}

type achievementUpdateBody struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	Points             *int   `json:"points"`
	CriterionThreshold *int   `json:"criterionThreshold"`
	Active             *bool  `json:"active"`
}

type myAchievementsDTO struct {
	Totals       xpDTO            `json:"totals"`
	Achievements []achievementDTO `json:"achievements"`
}

type xpDTO struct {
	ExperiencePoints       int `json:"experiencePoints"`
	CourseExperiencePoints int `json:"courseExperiencePoints"`
	ModuleExperiencePoints int `json:"moduleExperiencePoints"`
	AchievementPoints      int `json:"achievementPoints"`
}

func myAchievements(m application.MyAchievements) myAchievementsDTO {
	return myAchievementsDTO{Totals: xpDTO{ExperiencePoints: m.Totals.Total(), CourseExperiencePoints: m.Totals.Course,
		ModuleExperiencePoints: m.Totals.Module, AchievementPoints: m.Totals.Achievements}, Achievements: achievementList(m.Items)}
}

// ---- Rutas ----

type pathBody struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	CourseIDs   *[]string  `json:"courseIds"`
	RowVersion  rowVersion `json:"rowVersion"`
}

type pathCourseDTO struct {
	CourseID       string `json:"courseId"`
	Code           string `json:"code"`
	Title          string `json:"title"`
	Position       int    `json:"position"`
	CourseStatus   string `json:"courseStatus"`
	ProgressStatus string `json:"progressStatus"`
}

type pathProgressDTO struct {
	Status           string  `json:"status"`
	CompletedCourses int     `json:"completedCourses"`
	TotalCourses     int     `json:"totalCourses"`
	Percent          int     `json:"percent"`
	NextCourseID     *string `json:"nextCourseId"`
}

type pathDTO struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	RowVersion  string          `json:"rowVersion"`
	PublishedAt *string         `json:"publishedAt"`
	Courses     []pathCourseDTO `json:"courses"`
	Progress    pathProgressDTO `json:"progress"`
}

func path(o application.PathOutcome) pathDTO {
	p := o.Path
	out := pathDTO{ID: p.ID, Title: p.Title, Description: p.Description, Status: p.Status, RowVersion: itoa(p.RowVersion), PublishedAt: stampPtr(p.PublishedAt),
		Courses: make([]pathCourseDTO, 0, len(p.Courses)),
		Progress: pathProgressDTO{Status: o.Progress.Status, CompletedCourses: o.Progress.CompletedCourses, TotalCourses: o.Progress.TotalCourses,
			Percent: o.Progress.Percent, NextCourseID: nullable(o.Progress.NextCourseID)}}
	for _, c := range p.Courses {
		out.Courses = append(out.Courses, pathCourseDTO{CourseID: c.CourseID, Code: c.Code, Title: c.Title, Position: c.Position, CourseStatus: c.CourseStatus, ProgressStatus: c.Progress})
	}
	return out
}

// ---- Catálogo offline ----

type offlineVideoDTO struct {
	MediaID   string `json:"mediaId"`
	MIMEType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

type offlineLessonDTO struct {
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	Content          string            `json:"content"`
	DisplayOrder     int               `json:"displayOrder"`
	Video            *offlineVideoDTO  `json:"video"`
	ExternalVideo    *externalVideoDTO `json:"externalVideo"`
	OfflineAvailable bool              `json:"offlineAvailable"`
}

type offlineCourseDTO struct {
	Course    courseDTO          `json:"course"`
	Version   learnVersionDTO    `json:"version"`
	Lessons   []offlineLessonDTO `json:"lessons"`
	Questions []learnQuestionDTO `json:"questions"`
}

type offlineCatalogDTO struct {
	ETag        string             `json:"etag"`
	GeneratedAt string             `json:"generatedAt"`
	Courses     []offlineCourseDTO `json:"courses"`
}

func offlineCatalog(c application.OfflineCatalog) offlineCatalogDTO {
	out := offlineCatalogDTO{ETag: c.ETag, GeneratedAt: stamp(c.GeneratedAt), Courses: make([]offlineCourseDTO, 0, len(c.Courses))}
	for _, v := range c.Courses {
		base := courseView(v)
		item := offlineCourseDTO{Course: base.Course, Version: base.Version, Questions: base.Questions, Lessons: make([]offlineLessonDTO, 0, len(v.Lessons))}
		for _, l := range v.Lessons {
			lesson := offlineLessonDTO{ID: l.ID, Title: l.Title, Content: l.Content, DisplayOrder: l.DisplayOrder,
				ExternalVideo: externalVideo(l.ExternalVideo), OfflineAvailable: l.ExternalVideo == nil}
			if l.Video != nil {
				lesson.Video = &offlineVideoDTO{MediaID: l.Video.MediaID, MIMEType: l.Video.MIMEType, SizeBytes: l.Video.SizeBytes, SHA256: l.Video.SHA256}
			}
			item.Lessons = append(item.Lessons, lesson)
		}
		out.Courses = append(out.Courses, item)
	}
	return out
}

// ---- Verificación pública ----

type publicCertificateDTO struct {
	Status        string `json:"status"`
	CourseTitle   string `json:"courseTitle"`
	VersionNumber int    `json:"versionNumber"`
	IssuedAt      string `json:"issuedAt"`
	Organization  string `json:"organization"`
	Holder        string `json:"holder"`
}

func publicCertificate(c domain.PublicCertificate) publicCertificateDTO {
	return publicCertificateDTO{Status: c.Status, CourseTitle: c.CourseTitle, VersionNumber: c.VersionNumber, IssuedAt: stamp(c.IssuedAt),
		Organization: c.Organization, Holder: c.Holder}
}
