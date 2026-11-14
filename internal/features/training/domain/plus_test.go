package domain

import (
	"testing"
	"time"
)

func TestParseYouTubeURLAcceptsOnlyKnownFormats(t *testing.T) {
	const id = "dQw4w9WgXcQ"
	for _, raw := range []string{
		"https://www.youtube.com/watch?v=" + id,
		"https://youtube.com/watch?v=" + id + "&t=10s",
		"https://m.youtube.com/watch?v=" + id,
		"https://youtu.be/" + id,
		"https://youtu.be/" + id + "?si=abc",
		"https://www.youtube.com/shorts/" + id,
		"https://www.youtube-nocookie.com/embed/" + id,
	} {
		if got, ok := ParseYouTubeURL(raw); !ok || got != id {
			t.Fatal("debe aceptar", raw, got, ok)
		}
	}
	for _, raw := range []string{
		"", "dQw4w9WgXcQ", "https://evil.example/watch?v=" + id, "https://youtube.com.evil.example/watch?v=" + id,
		"https://www.youtube.com/watch?v=corto", "https://www.youtube.com/watch", "https://user@www.youtube.com/watch?v=" + id,
		"https://www.youtube.com:8443/watch?v=" + id, "javascript:alert(1)", "ftp://youtu.be/" + id, "https://youtu.be/" + id + "/extra",
		"https://www.youtube.com/embed/" + id, "https://www.youtube.com/shorts/" + id + "/x",
		"https://www.youtube.com/watch?v=" + id + "%00",
	} {
		if got, ok := ParseYouTubeURL(raw); ok {
			t.Fatal("debe rechazar", raw, got)
		}
	}
	if v, ok := NormalizeExternalVideo("YOUTUBE", id, ""); !ok || v.VideoID != id {
		t.Fatal("id directo")
	}
	if _, ok := NormalizeExternalVideo("YOUTUBE", id, "https://youtu.be/"+id); ok {
		t.Fatal("id y url a la vez")
	}
	if _, ok := NormalizeExternalVideo("VIMEO", id, ""); ok {
		t.Fatal("proveedor desconocido")
	}
}

func TestDraftRejectsLessonWithBothVideos(t *testing.T) {
	in := DraftInput{PassingScore: 80, RowVersion: 1, Questions: []Question{}, Lessons: []Lesson{{ID: "00000000-0000-4000-8000-000000000001", Title: "x", DisplayOrder: 1,
		VideoMediaID: "00000000-0000-4000-8000-000000000002", ExternalVideo: &ExternalVideo{Provider: ProviderYouTube, VideoID: "dQw4w9WgXcQ"}}}}
	if _, err := NormalizeDraft(in); err == nil {
		t.Fatal("video privado y externo a la vez")
	}
	in.Lessons[0].VideoMediaID = ""
	in.Lessons[0].ExternalVideo = &ExternalVideo{}
	if _, err := NormalizeDraft(in); err == nil {
		t.Fatal("marcador inválido")
	}
}

func TestLearnStatusAndAssignmentNormalization(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, c := range []struct {
		progress string
		due      *time.Time
		want     string
	}{
		{ProgressNotStarted, nil, LearnPending}, {ProgressInProgress, &future, LearnInProgress}, {ProgressInProgress, &past, LearnOverdue},
		{ProgressNotStarted, &past, LearnOverdue}, {ProgressCompleted, &past, LearnCompleted},
	} {
		if got := LearnStatus(c.progress, c.due, now); got != c.want {
			t.Fatal(c, got)
		}
	}
	good := NewAssignment{ID: "00000000-0000-4000-8000-000000000001", CourseID: "00000000-0000-4000-8000-000000000002", Target: AssignmentTarget{Type: TargetRole, Role: "CHEF"}}
	if _, err := good.Normalize(now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []AssignmentTarget{{Type: TargetUser}, {Type: TargetRole, Role: "OWNER"}, {Type: TargetLocation}, {Type: TargetUser, UserID: "a", Role: "CHEF"}, {Type: "ALL"}} {
		in := good
		in.Target = bad
		if _, err := in.Normalize(now); err == nil {
			t.Fatal("destinatario inválido", bad)
		}
	}
}

func TestAchievementCriteriaStreakAndHolder(t *testing.T) {
	day := time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC)
	if StreakDays([]time.Time{day, day.AddDate(0, 0, -1), day.AddDate(0, 0, -2), day.AddDate(0, 0, -4)}) != 3 {
		t.Fatal("racha de 3 días")
	}
	if StreakDays(nil) != 0 || StreakDays([]time.Time{day}) != 1 {
		t.Fatal("racha vacía o de un día")
	}
	a := Achievement{Criterion: CriterionCoursesCompleted, Threshold: 3, Active: true}
	if a.Met(AchievementStats{CoursesCompleted: 2}) || !a.Met(AchievementStats{CoursesCompleted: 3}) {
		t.Fatal("umbral")
	}
	a.Active = false
	if a.Met(AchievementStats{CoursesCompleted: 9}) {
		t.Fatal("inactiva")
	}
	if AbbreviateHolder("María González Pérez") != "María G." || AbbreviateHolder("Ana") != "A." || AbbreviateHolder("  ") != "" {
		t.Fatal("abreviatura")
	}
	if ValidVerificationCode("abc") || !ValidVerificationCode("ABCDEFGHIJKLMNOPQRSTUVWX") || ValidVerificationCode("ABCDEFGHIJKLMNOPQRSTUVW1") {
		t.Fatal("formato del código")
	}
	p := ComputePathProgress([]PathCourse{{CourseID: "a", CourseStatus: StatusPublished, Progress: ProgressCompleted}, {CourseID: "b", CourseStatus: StatusPublished, Progress: ProgressNotStarted}, {CourseID: "c", CourseStatus: StatusDraft}})
	if p.Percent != 50 || p.NextCourseID != "b" || p.Status != ProgressInProgress || p.TotalCourses != 2 {
		t.Fatal(p)
	}
}
