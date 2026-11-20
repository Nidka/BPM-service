package traininghttp

import (
	"context"
	"sort"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// plusState es el estado en memoria de asignaciones, insignias, rutas y constancias.
type plusState struct {
	assignments   []domain.Assignment
	ruleCalls     int
	locations     map[string]bool // "org|locationId"
	achievements  map[string][]domain.Achievement
	awards        map[string]time.Time // "org|user|achievementId"
	awardAttempt  map[string]string    // "org|user|achievementId" -> attemptId
	modules       map[string]int       // "org|user|lesson" -> xp
	paths         map[string]*domain.Path
	revoked       map[string]bool
	orderedPaths  []string
	assignmentOrg map[string]string
	lessonDone    map[string]string // "org|user|lesson" -> origen (VIEWED|MODULE_PASSED)
	lessonAt      map[string]time.Time
	zones         map[string]string // "org|user" -> zona de la primera sede activa
	activityErr   error
}

func newPlusState() plusState {
	return plusState{locations: map[string]bool{}, achievements: map[string][]domain.Achievement{}, awards: map[string]time.Time{},
		awardAttempt: map[string]string{}, modules: map[string]int{}, paths: map[string]*domain.Path{}, revoked: map[string]bool{}, assignmentOrg: map[string]string{}, lessonDone: map[string]string{}, lessonAt: map[string]time.Time{}, zones: map[string]string{}}
}

func (f *fakeRepo) PublishedCourse(_ context.Context, org, courseID string) (domain.Course, error) {
	c, err := f.course(org, courseID)
	if err != nil {
		return domain.Course{}, err
	}
	if c.c.Status != domain.StatusPublished {
		return domain.Course{}, domain.ErrCourseNotPublished
	}
	return c.c, nil
}

func (f *fakeRepo) ResolveTarget(_ context.Context, org string, t domain.AssignmentTarget) error {
	if t.UserID != "" {
		if _, ok := f.roles[org+"|"+t.UserID]; !ok {
			return domain.ErrAssigneeNotFound
		}
	}
	if t.LocationID != "" && !f.plus.locations[org+"|"+t.LocationID] {
		return domain.ErrLocationNotFound
	}
	return nil
}

func (f *fakeRepo) FindAssignment(_ context.Context, org, id string) (domain.Assignment, bool, error) {
	for _, a := range f.plus.assignments {
		if a.ID == id && f.plus.assignmentOrg[a.ID] == org {
			return a, true, nil
		}
	}
	return domain.Assignment{}, false, nil
}

func (f *fakeRepo) InsertAssignment(_ context.Context, org, _ string, in domain.NewAssignment) (domain.Assignment, error) {
	for _, a := range f.plus.assignments {
		if f.plus.assignmentOrg[a.ID] == org && a.Status == domain.AssignmentActive && a.CourseID == in.CourseID && a.Target == in.Target {
			return domain.Assignment{}, domain.ErrAssignmentExists
		}
	}
	c, _ := f.course(org, in.CourseID)
	a := domain.Assignment{NewAssignment: in, CourseCode: c.c.Code, CourseTitle: c.c.Title, Status: domain.AssignmentActive, CreatedAt: time.Now().UTC()}
	f.plus.assignmentOrg[a.ID] = org
	f.plus.assignments = append(f.plus.assignments, a)
	return a, nil
}

func (f *fakeRepo) EnsureAssignmentAlertRule(context.Context, string, string) error {
	f.plus.ruleCalls++
	return nil
}

func (f *fakeRepo) ListAssignments(_ context.Context, org string, filter domain.AssignmentFilter, _ pagination.Window) ([]domain.Assignment, error) {
	out := []domain.Assignment{}
	for _, a := range f.plus.assignments {
		if f.plus.assignmentOrg[a.ID] == org && (filter.Status == "" || a.Status == filter.Status) && (filter.CourseID == "" || a.CourseID == filter.CourseID) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeRepo) CancelAssignment(_ context.Context, org, _, id string, now time.Time) (domain.Assignment, error) {
	for i, a := range f.plus.assignments {
		if a.ID == id && f.plus.assignmentOrg[a.ID] == org {
			if a.Status == domain.AssignmentActive {
				a.Status, a.CancelledAt = domain.AssignmentCancelled, &now
				f.plus.assignments[i] = a
			}
			return a, nil
		}
	}
	return domain.Assignment{}, domain.ErrAssignmentNotFound
}

func (f *fakeRepo) MyAssignments(_ context.Context, org, user string, _ pagination.Window) ([]domain.MyAssignment, error) {
	role := f.roles[org+"|"+user]
	byCourse := map[string]domain.MyAssignment{}
	for _, a := range f.plus.assignments {
		t := a.Target
		applies := f.plus.assignmentOrg[a.ID] == org && a.Status == domain.AssignmentActive &&
			(t.Type == domain.TargetUser && t.UserID == user || t.Type == domain.TargetRole && t.Role == role)
		c, err := f.course(org, a.CourseID)
		if !applies || err != nil || c.c.Status != domain.StatusPublished {
			continue
		}
		p, _ := f.Progress(context.Background(), org, user, a.CourseID)
		byCourse[a.CourseID] = domain.MyAssignment{AssignmentID: a.ID, Course: c.c, DueAt: a.DueAt, AssignedAt: a.CreatedAt, ProgressStatus: p.Status, CompletedAt: p.CompletedAt, BestScore: p.BestScore, Percent: f.percentOf(org, user, a.CourseID)}
	}
	out := []domain.MyAssignment{}
	for _, m := range byCourse {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AssignmentID < out[j].AssignmentID })
	return out, nil
}

func (f *fakeRepo) AssignedCourseIDs(ctx context.Context, org, user string, limit int) ([]string, error) {
	items, _ := f.MyAssignments(ctx, org, user, pagination.Window{})
	out := []string{}
	for _, m := range items {
		out = append(out, m.Course.ID)
	}
	return out, nil
}

func (f *fakeRepo) EnsureAchievements(_ context.Context, org string) error {
	if _, ok := f.plus.achievements[org]; ok {
		return nil
	}
	for i, a := range domain.DefaultAchievements() {
		a.ID = uid(7, i+1)
		f.plus.achievements[org] = append(f.plus.achievements[org], a)
	}
	return nil
}

func (f *fakeRepo) Achievements(_ context.Context, org, user string) ([]domain.Achievement, error) {
	out := []domain.Achievement{}
	for _, a := range f.plus.achievements[org] {
		if at, ok := f.plus.awards[org+"|"+user+"|"+a.ID]; ok {
			t := at
			a.AwardedAt = &t
		}
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeRepo) AchievementStats(_ context.Context, org, user string) (domain.AchievementStats, error) {
	var st domain.AchievementStats
	for key, p := range f.progress {
		if len(key) > len(org+"|"+user+"|") && key[:len(org+"|"+user+"|")] == org+"|"+user+"|" && p.Status == domain.ProgressCompleted {
			st.CoursesCompleted++
		}
	}
	for _, a := range f.attempts {
		if a.UserID == user && (a.AttemptType == domain.AttemptFinal || a.AttemptType == domain.AttemptModule) && a.Score == 100 {
			st.PerfectAttempts++
		}
	}
	st.StreakDays = 1
	return st, nil
}

func (f *fakeRepo) GrantAchievement(_ context.Context, org, user, id, attemptID string) (bool, error) {
	key := org + "|" + user + "|" + id
	if _, ok := f.plus.awards[key]; ok {
		return false, nil
	}
	f.plus.awards[key] = time.Now().UTC()
	f.plus.awardAttempt[key] = attemptID
	return true, nil
}

func (f *fakeRepo) AttemptAchievements(_ context.Context, org, attemptID string) ([]domain.Achievement, error) {
	out := []domain.Achievement{}
	for key, id := range f.plus.awardAttempt {
		if id != attemptID {
			continue
		}
		for _, a := range f.plus.achievements[org] {
			if len(key) >= len(a.ID) && key[len(key)-len(a.ID):] == a.ID {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateAchievement(_ context.Context, org, _, id string, u domain.AchievementUpdate) (domain.Achievement, error) {
	for i, a := range f.plus.achievements[org] {
		if a.ID == id {
			a.Name, a.Description, a.Points, a.Threshold, a.Active = u.Name, u.Description, u.Points, u.Threshold, u.Active
			f.plus.achievements[org][i] = a
			return a, nil
		}
	}
	return domain.Achievement{}, domain.ErrAchievementNotFound
}

func (f *fakeRepo) XPTotals(_ context.Context, org, user string) (domain.XPTotals, error) {
	var x domain.XPTotals
	for key, p := range f.progress {
		if len(key) > len(org+"|"+user+"|") && key[:len(org+"|"+user+"|")] == org+"|"+user+"|" {
			x.Course += p.ExperiencePoints
		}
	}
	for key, xp := range f.plus.modules {
		if key[:len(org+"|"+user+"|")] == org+"|"+user+"|" {
			x.Module += xp
		}
	}
	for key := range f.plus.awards {
		for _, a := range f.plus.achievements[org] {
			if key == org+"|"+user+"|"+a.ID {
				x.Achievements += a.Points
			}
		}
	}
	return x, nil
}

func (f *fakeRepo) SaveModuleResult(_ context.Context, org, user, _, lessonID string, _ int, _ bool, xp int) error {
	key := org + "|" + user + "|" + lessonID
	if xp > f.plus.modules[key] {
		f.plus.modules[key] = xp
	}
	return nil
}

func (f *fakeRepo) InsertPath(_ context.Context, org, _ string, in domain.PathInput) error {
	if _, ok := f.plus.paths[in.ID]; ok {
		return domain.ErrIdempotencyConflict
	}
	p := &domain.Path{ID: in.ID, Title: in.Title, Description: in.Description, Status: domain.PathDraft, RowVersion: 1}
	f.fillPath(org, p, in.CourseIDs)
	f.plus.paths[in.ID] = p
	f.plus.orderedPaths = append(f.plus.orderedPaths, org+"|"+in.ID)
	return nil
}

func (f *fakeRepo) fillPath(org string, p *domain.Path, ids []string) {
	p.Courses = []domain.PathCourse{}
	for i, id := range ids {
		if c, err := f.course(org, id); err == nil {
			p.Courses = append(p.Courses, domain.PathCourse{CourseID: id, Code: c.c.Code, Title: c.c.Title, Position: i + 1, CourseStatus: c.c.Status, Progress: domain.ProgressNotStarted})
		}
	}
}

func (f *fakeRepo) pathOf(org, id string) *domain.Path {
	for _, key := range f.plus.orderedPaths {
		if key == org+"|"+id {
			return f.plus.paths[id]
		}
	}
	return nil
}

func (f *fakeRepo) FindPath(_ context.Context, org, _, id string, _ bool) (domain.Path, bool, error) {
	if p := f.pathOf(org, id); p != nil {
		return *p, true, nil
	}
	return domain.Path{}, false, nil
}

func (f *fakeRepo) ListPaths(_ context.Context, org, _, status string, _ pagination.Window) ([]domain.Path, error) {
	out := []domain.Path{}
	for _, key := range f.plus.orderedPaths {
		if p := f.plus.paths[key[len(org)+1:]]; key[:len(org)] == org && (status == "" || p.Status == status) {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *fakeRepo) ReplacePath(_ context.Context, org, _, id string, rowVersion int, in domain.PathInput) error {
	p := f.pathOf(org, id)
	if p == nil || p.RowVersion != rowVersion {
		return domain.ErrPathConflict
	}
	p.Title, p.Description = in.Title, in.Description
	p.RowVersion++
	f.fillPath(org, p, in.CourseIDs)
	return nil
}

func (f *fakeRepo) SetPathStatus(_ context.Context, org, _, id, status string, now time.Time) error {
	p := f.pathOf(org, id)
	p.Status = status
	if status == domain.PathPublished {
		p.PublishedAt = &now
	}
	p.RowVersion++
	return nil
}

func (f *fakeRepo) CourseStatuses(_ context.Context, org string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if c, err := f.course(org, id); err == nil {
			out[id] = c.c.Status
		}
	}
	return out, nil
}

func (f *fakeRepo) CertificateByCode(_ context.Context, code string) (domain.PublicCertificate, error) {
	for _, c := range f.certs {
		if c.c.VerificationCode != code {
			continue
		}
		course := f.courses[c.course]
		out := domain.PublicCertificate{CourseTitle: course.c.Title, VersionNumber: 1, IssuedAt: c.c.IssuedAt, Organization: "Empresa " + c.org, Holder: "Ana P.", Status: domain.CertificateValid}
		if f.plus.revoked[code] {
			out.Status = domain.CertificateRevoked
		}
		return out, nil
	}
	return domain.PublicCertificate{}, domain.ErrCertificateNotFound
}

// percentOf replica el porcentaje de MyAssignments: 100 si está completado y, si no, lecciones completadas / totales.
func (f *fakeRepo) percentOf(org, user, courseID string) int {
	_, v, err := f.publishedOf(org, courseID)
	if err != nil {
		return 0
	}
	p, _ := f.Progress(context.Background(), org, user, courseID)
	ids, done, _ := f.LessonProgress(context.Background(), org, user, v.v.ID)
	return p.WithLessons(ids, done).Percent()
}

func (f *fakeRepo) LessonProgress(_ context.Context, org, user, versionID string) ([]string, []string, error) {
	v := f.versionByID(org, versionID)
	if v == nil {
		return []string{}, []string{}, nil
	}
	lessons := append([]domain.Lesson(nil), v.lessons...)
	sort.Slice(lessons, func(i, j int) bool { return lessons[i].DisplayOrder < lessons[j].DisplayOrder })
	ids, done := []string{}, []string{}
	for _, l := range lessons {
		ids = append(ids, l.ID)
		if _, ok := f.plus.lessonDone[org+"|"+user+"|"+l.ID]; ok {
			done = append(done, l.ID)
		}
	}
	return ids, done, nil
}

func (f *fakeRepo) CompleteLesson(_ context.Context, org, user, _, lessonID, source string) error {
	key := org + "|" + user + "|" + lessonID
	if current, ok := f.plus.lessonDone[key]; !ok || (current == domain.LessonViewed && source == domain.LessonModulePassed) {
		f.plus.lessonDone[key] = source
	}
	if _, ok := f.plus.lessonAt[key]; !ok {
		f.plus.lessonAt[key] = time.Now().UTC()
	}
	return nil
}

func (f *fakeRepo) ModuleXP(_ context.Context, org, user, lessonID string) (int, error) {
	return f.plus.modules[org+"|"+user+"|"+lessonID], nil
}

func (f *fakeRepo) LearningActivity(_ context.Context, org, user string, since time.Time) ([]time.Time, error) {
	if f.plus.activityErr != nil {
		return nil, f.plus.activityErr
	}
	out := []time.Time{}
	for _, a := range f.attempts {
		if a.UserID == user && !a.CompletedAt.Before(since) {
			out = append(out, a.CompletedAt)
		}
	}
	prefix := org + "|" + user + "|"
	for key, at := range f.plus.lessonAt {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix && !at.Before(since) {
			out = append(out, at)
		}
	}
	return out, nil
}

func (f *fakeRepo) UserTimezone(_ context.Context, org, user string) (string, error) {
	return f.plus.zones[org+"|"+user], nil
}

func (f *fakeRepo) PublishedPathsWithCourse(_ context.Context, org, user, courseID string) ([]domain.Path, error) {
	out := []domain.Path{}
	for _, key := range f.plus.orderedPaths {
		if key[:len(org)] != org || key[len(org)] != '|' {
			continue
		}
		p := *f.plus.paths[key[len(org)+1:]]
		if p.Status != domain.PathPublished {
			continue
		}
		has := false
		p.Courses = append([]domain.PathCourse(nil), p.Courses...)
		for i := range p.Courses {
			has = has || p.Courses[i].CourseID == courseID
			progress, _ := f.Progress(context.Background(), org, user, p.Courses[i].CourseID)
			p.Courses[i].Progress = progress.Status
		}
		if has {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PublishedAt.After(*out[j].PublishedAt) })
	return out, nil
}
