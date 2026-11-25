package traininghttp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// fakeVersion y fakeCourse reproducen en memoria el modelo versionado de la Academia.
type fakeVersion struct {
	v         domain.Version
	status    string
	lessons   []domain.Lesson
	questions []domain.Question
}

type fakeCourse struct {
	org      string
	c        domain.Course
	versions []*fakeVersion
}

type fakeCertificate struct {
	org, user, course, version string
	c                          domain.Certificate
}

// fakeRepo implementa application.UnitOfWork y application.Store en memoria.
// Aplica los mismos filtros de tenant que el repositorio PostgreSQL.
type fakeRepo struct {
	mu       sync.Mutex
	roles    map[string]string // "org|usuario" -> rol de membresía
	supers   map[string]bool
	courses  map[string]*fakeCourse
	media    map[string]domain.VideoFile
	progress map[string]domain.Progress
	attempts []application.AttemptRecord
	results  map[string][]domain.Result
	certs    []fakeCertificate
	audits   []string
	fail     error
	seq      int
	plus     plusState
	bank     bankState
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{roles: map[string]string{}, supers: map[string]bool{}, courses: map[string]*fakeCourse{}, media: map[string]domain.VideoFile{},
		progress: map[string]domain.Progress{}, results: map[string][]domain.Result{}, plus: newPlusState(), bank: newBankState()}
}

func (f *fakeRepo) Within(_ context.Context, fn func(application.Store) error) error {
	if f.fail != nil {
		return f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f)
}

func (f *fakeRepo) next() string {
	f.seq++
	return fmt.Sprintf("%08d-0000-4000-8000-%012d", 9, f.seq)
}

func (f *fakeRepo) Actor(_ context.Context, user, org string) (domain.Actor, error) {
	if f.supers[user] {
		return domain.Actor{ID: user, Role: "SUPER_ADMIN"}, nil
	}
	role, ok := f.roles[org+"|"+user]
	if !ok {
		return domain.Actor{}, domain.ErrDenied
	}
	return domain.Actor{ID: user, Role: role}, nil
}

func (f *fakeRepo) Audit(_ context.Context, org, user, action, _, id string) error {
	f.audits = append(f.audits, org+"|"+user+"|"+action+"|"+id)
	return nil
}

func (f *fakeRepo) course(org, id string) (*fakeCourse, error) {
	c, ok := f.courses[id]
	if !ok || c.org != org {
		return nil, domain.ErrCourseNotFound
	}
	return c, nil
}

func (c *fakeCourse) withStatus(status string) *fakeVersion {
	for _, v := range c.versions {
		if v.status == status {
			return v
		}
	}
	return nil
}

func (f *fakeRepo) publishedOf(org, courseID string) (*fakeCourse, *fakeVersion, error) {
	c, err := f.course(org, courseID)
	if err != nil {
		return nil, nil, err
	}
	v := c.withStatus(domain.StatusPublished)
	if v == nil || c.c.Status != domain.StatusPublished {
		return nil, nil, domain.ErrCourseNotFound
	}
	return c, v, nil
}

func (f *fakeRepo) CourseView(_ context.Context, org, userID, courseID string) (domain.CourseView, error) {
	c, v, err := f.publishedOf(org, courseID)
	if err != nil {
		return domain.CourseView{}, err
	}
	view := domain.CourseView{Course: c.c, Version: v.v, Progress: domain.Progress{Status: domain.ProgressNotStarted}}
	for _, l := range v.lessons {
		item := domain.LearnLesson{Lesson: l}
		if m, ok := f.media[l.VideoMediaID]; ok && m.OrganizationID == org && m.Status == "AVAILABLE" {
			item.Video = &domain.Video{MediaID: m.ID, MIMEType: m.MIMEType, SizeBytes: m.SizeBytes}
		}
		view.Lessons = append(view.Lessons, item)
	}
	for _, q := range v.questions {
		view.Questions = append(view.Questions, domain.PublicQuestion{ID: q.ID, LessonID: q.LessonID, Text: q.Text, Options: q.Options, DisplayOrder: q.DisplayOrder, BankVersion: q.BankVersion})
		if q.BankVersion > 0 {
			if view.Bank == nil {
				view.Bank = &domain.BankInfo{ItemsPerAttempt: domain.ItemsPerEvaluation}
			}
			view.Bank.PoolSize++
			view.Bank.Version = q.BankVersion
		}
	}
	if userID != "" {
		view.Progress, _ = f.Progress(context.Background(), org, userID, courseID)
		ids, done, _ := f.LessonProgress(context.Background(), org, userID, v.v.ID)
		view.Progress = view.Progress.WithLessons(ids, done)
		view.Certificate, _ = f.LatestCertificate(context.Background(), org, userID, courseID)
	}
	return view, nil
}

func (f *fakeRepo) PublishedVersion(_ context.Context, org, courseID string) (domain.Version, error) {
	_, v, err := f.publishedOf(org, courseID)
	if err != nil {
		return domain.Version{}, err
	}
	return v.v, nil
}

func (f *fakeRepo) versionByID(org, versionID string) *fakeVersion {
	for _, c := range f.courses {
		if c.org != org {
			continue
		}
		for _, v := range c.versions {
			if v.v.ID == versionID {
				return v
			}
		}
	}
	return nil
}

func (f *fakeRepo) LessonInVersion(_ context.Context, org, versionID, lessonID string) error {
	if v := f.versionByID(org, versionID); v != nil {
		for _, l := range v.lessons {
			if l.ID == lessonID {
				return nil
			}
		}
	}
	return domain.ErrLessonNotFound
}

func (f *fakeRepo) LessonVideo(_ context.Context, org, versionID, lessonID string) (domain.VideoFile, error) {
	if v := f.versionByID(org, versionID); v != nil {
		for _, l := range v.lessons {
			if m, ok := f.media[l.VideoMediaID]; ok && l.ID == lessonID && m.OrganizationID == org && m.Status == "AVAILABLE" {
				return m, nil
			}
		}
	}
	return domain.VideoFile{}, domain.ErrVideoNotFound
}

func (f *fakeRepo) Question(_ context.Context, org, versionID, questionID string) (domain.Question, error) {
	if v := f.versionByID(org, versionID); v != nil {
		for _, q := range v.questions {
			if q.ID == questionID {
				return q, nil
			}
		}
	}
	return domain.Question{}, domain.ErrQuestionNotFound
}

func (f *fakeRepo) Questions(_ context.Context, org, versionID string) ([]domain.Question, error) {
	if v := f.versionByID(org, versionID); v != nil {
		return append([]domain.Question(nil), v.questions...), nil
	}
	return nil, nil
}

func (f *fakeRepo) LockLearner(context.Context, string, string, string) error { return nil }

func (f *fakeRepo) Progress(_ context.Context, org, user, courseID string) (domain.Progress, error) {
	if p, ok := f.progress[org+"|"+user+"|"+courseID]; ok {
		return p, nil
	}
	return domain.Progress{Status: domain.ProgressNotStarted}, nil
}

func (f *fakeRepo) SaveProgress(_ context.Context, org, user, courseID string, p domain.Progress) error {
	f.progress[org+"|"+user+"|"+courseID] = p
	return nil
}

func (f *fakeRepo) FindAttempt(_ context.Context, _, attemptID, operationID string) ([]application.AttemptRecord, error) {
	var out []application.AttemptRecord
	for _, a := range f.attempts {
		if a.ID == attemptID || a.ClientOperationID == operationID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeRepo) InsertAttempt(_ context.Context, _ string, r application.AttemptRecord, results []domain.Result) error {
	f.attempts = append(f.attempts, r)
	f.results[r.ID] = results
	return nil
}

func (f *fakeRepo) AttemptResults(_ context.Context, _, attemptID string) ([]domain.Result, error) {
	return f.results[attemptID], nil
}

func (f *fakeRepo) IssueCertificate(_ context.Context, org, user, versionID, _, code string) error {
	for _, c := range f.certs {
		if c.org == org && c.user == user && c.version == versionID {
			return nil
		}
	}
	courseID := ""
	for id, c := range f.courses {
		for _, v := range c.versions {
			if v.v.ID == versionID {
				courseID = id
			}
		}
	}
	f.certs = append(f.certs, fakeCertificate{org: org, user: user, course: courseID, version: versionID, c: domain.Certificate{ID: f.next(), VerificationCode: code, IssuedAt: time.Now().UTC()}})
	return nil
}

func (f *fakeRepo) LatestCertificate(_ context.Context, org, user, courseID string) (*domain.Certificate, error) {
	for i := len(f.certs) - 1; i >= 0; i-- {
		if c := f.certs[i]; c.org == org && c.user == user && c.course == courseID {
			cert := c.c
			return &cert, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) ListCourses(_ context.Context, org, status string) ([]domain.CourseSummary, error) {
	out := []domain.CourseSummary{}
	for _, c := range f.courses {
		if c.org != org || (status != "" && c.c.Status != status) {
			continue
		}
		item := domain.CourseSummary{ID: c.c.ID, Code: c.c.Code, Title: c.c.Title, Status: c.c.Status, HasDraft: c.withStatus(domain.StatusDraft) != nil}
		if v := c.withStatus(domain.StatusPublished); v != nil {
			n := v.v.Number
			item.PublishedVersionNumber = &n
		}
		out = append(out, item)
	}
	return out, nil
}

func (f *fakeRepo) CreateCourse(_ context.Context, org, _ string, in domain.NewCourse) (domain.Course, error) {
	for _, c := range f.courses {
		if c.org == org && c.c.Code == in.Code {
			return domain.Course{}, domain.ErrCourseCodeTaken
		}
	}
	course := domain.Course{ID: f.next(), Code: in.Code, Title: in.Title, Description: in.Description, EstimatedDurationMinutes: in.EstimatedDurationMinutes, Status: domain.StatusDraft}
	f.courses[course.ID] = &fakeCourse{org: org, c: course}
	return course, nil
}

func (f *fakeRepo) LockCourse(_ context.Context, org, courseID string) (domain.Course, error) {
	c, err := f.course(org, courseID)
	if err != nil {
		return domain.Course{}, err
	}
	return c.c, nil
}

func (f *fakeRepo) Draft(_ context.Context, org string, course domain.Course) (domain.Draft, bool, error) {
	c, err := f.course(org, course.ID)
	if err != nil {
		return domain.Draft{}, false, err
	}
	v := c.withStatus(domain.StatusDraft)
	if v == nil {
		return domain.Draft{}, false, nil
	}
	return domain.Draft{Course: c.c, Version: v.v, Lessons: append([]domain.Lesson(nil), v.lessons...), Questions: append([]domain.Question(nil), v.questions...)}, true, nil
}

func (f *fakeRepo) CreateDraft(_ context.Context, org, _ string, course domain.Course) (domain.Draft, error) {
	c, err := f.course(org, course.ID)
	if err != nil {
		return domain.Draft{}, err
	}
	version := &fakeVersion{status: domain.StatusDraft, v: domain.Version{ID: f.next(), Number: len(c.versions) + 1, PassingScore: domain.DefaultPassScore, RowVersion: 1}}
	if p := c.withStatus(domain.StatusPublished); p != nil {
		version.v.PassingScore = p.v.PassingScore
		ids := map[string]string{}
		for _, l := range p.lessons {
			id := f.next()
			ids[l.ID] = id
			l.ID = id
			version.lessons = append(version.lessons, l)
		}
		for _, q := range p.questions {
			q.ID = f.next()
			if q.LessonID != "" {
				q.LessonID = ids[q.LessonID]
			}
			version.questions = append(version.questions, q)
		}
	}
	c.versions = append(c.versions, version)
	return domain.Draft{Course: c.c, Version: version.v, Lessons: version.lessons, Questions: version.questions}, nil
}

func (f *fakeRepo) ReplaceDraft(_ context.Context, org, _ string, draft domain.Draft, in domain.DraftInput) (domain.Draft, error) {
	v := f.versionByID(org, draft.Version.ID)
	if v == nil || v.v.RowVersion != draft.Version.RowVersion {
		return draft, domain.ErrDraftConflict
	}
	v.v.RowVersion++
	v.v.PassingScore = in.PassingScore
	v.lessons, v.questions = in.Lessons, in.Questions
	draft.Version, draft.Lessons, draft.Questions = v.v, in.Lessons, in.Questions
	return draft, nil
}

func (f *fakeRepo) VideoStatuses(_ context.Context, org string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if m, ok := f.media[id]; ok && m.OrganizationID == org {
			out[id] = m.Status
		}
	}
	return out, nil
}

func (f *fakeRepo) PublishDraft(_ context.Context, org, _ string, draft domain.Draft, now time.Time) (domain.Course, domain.Version, error) {
	c, err := f.course(org, draft.Course.ID)
	if err != nil {
		return domain.Course{}, domain.Version{}, err
	}
	if old := c.withStatus(domain.StatusPublished); old != nil {
		old.status = domain.StatusArchived
	}
	v := f.versionByID(org, draft.Version.ID)
	v.status = domain.StatusPublished
	v.v.PublishedAt = &now
	c.c.Status = domain.StatusPublished
	return c.c, v.v, nil
}

func (f *fakeRepo) Video(_ context.Context, org, id string, _ bool) (domain.VideoFile, error) {
	if m, ok := f.media[id]; ok && m.OrganizationID == org {
		return m, nil
	}
	return domain.VideoFile{}, domain.ErrVideoNotFound
}

func (f *fakeRepo) ReserveVideo(_ context.Context, org, _ string, file domain.VideoFile) (domain.VideoFile, error) {
	if m, ok := f.media[file.ID]; ok {
		if m.OrganizationID != org {
			return domain.VideoFile{}, domain.ErrVideoConflict
		}
		return m, m.Match(domain.VideoUploadInput{ID: file.ID, OriginalName: file.OriginalName, MIMEType: file.MIMEType, SizeBytes: file.SizeBytes, SHA256: file.SHA256})
	}
	file.Status, file.CreatedAt = "PENDING_UPLOAD", time.Now().UTC()
	f.media[file.ID] = file
	return file, nil
}

func (f *fakeRepo) SetVideoStatus(_ context.Context, org, _ string, file domain.VideoFile, status string) (domain.VideoFile, error) {
	m := f.media[file.ID]
	m.Status = status
	if status == "AVAILABLE" {
		now := time.Now().UTC()
		m.UploadedAt = &now
	}
	f.media[file.ID] = m
	return m, nil
}

// fakeVideos reemplaza a R2: devuelve URL con una firma reconocible para comprobar que no se registra.
type fakeVideos struct {
	inspection contracts.VideoInspection
	inspectErr error
	downloads  int
}

const signedSecret = "https://signed.example/object?X-Amz-Signature=SECRET"

func (v *fakeVideos) PresignVideoUpload(_ context.Context, r contracts.UploadRequest) (contracts.SignedRequest, error) {
	return contracts.SignedRequest{URL: signedSecret, Headers: map[string][]string{"Content-Type": {r.ContentType}, "If-None-Match": {"*"}, "Host": {"x"}, "Content-Length": {"1"}}, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}, nil
}

func (v *fakeVideos) PresignVideoDownload(context.Context, string) (contracts.SignedRequest, error) {
	v.downloads++
	return contracts.SignedRequest{URL: signedSecret, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}, nil
}

func (v *fakeVideos) InspectVideo(context.Context, string) (contracts.VideoInspection, error) {
	return v.inspection, v.inspectErr
}

var _ application.Store = (*fakeRepo)(nil)
var _ contracts.VideoStorage = (*fakeVideos)(nil)
