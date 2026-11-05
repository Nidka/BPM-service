package application

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
)

const (
	testRunID  = "10000000-0000-0000-0000-000000000001"
	testItemID = "20000000-0000-0000-0000-000000000001"
)

type fakeWork struct{ store *fakeStore }

func (w fakeWork) Within(_ context.Context, fn func(Store) error) error { return fn(w.store) }

type fakeStore struct {
	schedule       domain.Assignment
	periodRuns     map[string]domain.AssignedRun
	alreadyStarted bool
	actor          domain.Actor
	run            domain.Run
	saved, sent    bool
	score          float64
	createTemplate bool
}

func (f *fakeStore) StartedRun(context.Context, string, string) (bool, error) {
	return f.alreadyStarted, nil
}
func (f *fakeStore) RunSchedule(context.Context, domain.Actor, string, domain.Run) (domain.Assignment, error) {
	return f.schedule, nil
}
func (f *fakeStore) PeriodRuns(context.Context, string, []domain.AssignmentPeriod) (map[string]domain.AssignedRun, error) {
	return f.periodRuns, nil
}

func (f *fakeStore) Actor(context.Context, string, string) (domain.Actor, error)        { return f.actor, nil }
func (f *fakeStore) LocationAccess(context.Context, domain.Actor, string, string) error { return nil }
func (f *fakeStore) Catalog(context.Context, string) ([]domain.Template, error)         { return nil, nil }
func (f *fakeStore) CreateTemplate(context.Context, domain.Actor, string, domain.Template) error {
	f.createTemplate = true
	return nil
}
func (f *fakeStore) CreateRun(_ context.Context, _ domain.Actor, run domain.Run) (domain.Run, error) {
	return run, nil
}
func (f *fakeStore) Run(context.Context, domain.Actor, string, string, bool) (domain.Run, error) {
	return f.run, nil
}
func (f *fakeStore) Replay(context.Context, string, string, string) (domain.Run, bool, error) {
	return domain.Run{}, false, nil
}
func (f *fakeStore) SaveAnswers(_ context.Context, _ domain.Actor, run domain.Run, answers []domain.Answer, _, _ string) (domain.Run, error) {
	f.saved = true
	run.Answers = answers
	return run, nil
}
func (f *fakeStore) Submit(_ context.Context, _ domain.Actor, run domain.Run, score float64, _, _ string) (domain.Run, error) {
	f.sent = true
	f.score = score
	return run, nil
}

func TestTemplateRoleAndValidation(t *testing.T) {
	v := domain.Template{ID: testRunID, VersionID: "30000000-0000-0000-0000-000000000001", Name: "Control de cocina", Area: "Cocina",
		Items: []domain.Item{{ID: testItemID, SectionName: "Higiene", Label: "Lavado de manos", ResponseType: "COMPLIANCE", DisplayOrder: 1, Required: true}}}
	f := &fakeStore{actor: domain.Actor{ID: "worker", Role: "WORKER"}}
	s := New(fakeWork{f})
	if _, err := s.CreateTemplate(context.Background(), "worker", "org", v); !errors.Is(err, domain.ErrDenied) || f.createTemplate {
		t.Fatal("worker published a template", err)
	}
	f.actor.Role = "ADMIN"
	created, err := s.CreateTemplate(context.Background(), "admin", "org", v)
	if err != nil || !f.createTemplate || created.VersionNumber != 1 {
		t.Fatal("admin could not publish", err)
	}
	v.Items[0].EvidenceRequiredOnFailure = true
	_, err = s.CreateTemplate(context.Background(), "admin", "org", v)
	if err != nil {
		t.Fatal("no permitió configurar evidencia", err)
	}
	v.Items[0].ResponseType = "TEXT"
	_, err = s.CreateTemplate(context.Background(), "admin", "org", v)
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Fields["items"] == "" {
		t.Fatal("evidencia sin criterio de incumplimiento", err)
	}
}

func TestSaveAnswersVersionAndOwnership(t *testing.T) {
	f := &fakeStore{actor: domain.Actor{ID: "worker", Role: "CHEF"}, run: domain.Run{
		ID: testRunID, PerformedBy: "worker", Status: "DRAFT", RowVersion: 2,
		Items: []domain.Item{{ID: testItemID, ResponseType: "COMPLIANCE"}},
	}}
	s := New(fakeWork{f})
	answers := []domain.Answer{{ItemID: testItemID, ComplianceResult: "COMPLIANT"}}
	_, err := s.SaveAnswers(context.Background(), "worker", "org", testRunID, 1, "operation-1", answers)
	if !errors.Is(err, domain.ErrVersionConflict) || f.saved {
		t.Fatal("stale write accepted", err)
	}
	f.run.PerformedBy = "someone-else"
	_, err = s.SaveAnswers(context.Background(), "worker", "org", testRunID, 2, "operation-2", answers)
	if !errors.Is(err, domain.ErrDenied) || f.saved {
		t.Fatal("foreign write accepted", err)
	}
	f.run.PerformedBy = "worker"
	_, err = s.SaveAnswers(context.Background(), "worker", "org", testRunID, 2, "operation-3", answers)
	if err != nil || !f.saved {
		t.Fatal("valid draft not saved", err)
	}
}

func TestSubmitCalculatesServerScore(t *testing.T) {
	numberID := "20000000-0000-0000-0000-000000000002"
	naID := "20000000-0000-0000-0000-000000000003"
	textID := "20000000-0000-0000-0000-000000000004"
	min, max, measured := 0.0, 5.0, 8.0
	f := &fakeStore{actor: domain.Actor{ID: "worker", Role: "CHEF"}, run: domain.Run{
		ID: testRunID, PerformedBy: "worker", Status: "DRAFT", RowVersion: 1,
		Items: []domain.Item{
			{ID: testItemID, ResponseType: "COMPLIANCE", Required: true},
			{ID: numberID, ResponseType: "TEMPERATURE", Required: true, MinimumValue: &min, MaximumValue: &max},
			{ID: naID, ResponseType: "COMPLIANCE", Required: true},
			{ID: textID, ResponseType: "TEXT", Required: true},
		},
		Answers: []domain.Answer{
			{ItemID: testItemID, ComplianceResult: "COMPLIANT"},
			{ItemID: numberID, NumericValue: &measured},
			{ItemID: naID, ComplianceResult: "NOT_APPLICABLE"},
			{ItemID: textID, TextValue: "Observado"},
		},
	}}
	s := New(fakeWork{f})
	_, err := s.Submit(context.Background(), "worker", "org", testRunID, 1, "submit-001")
	if err != nil || !f.sent || f.score != 50 {
		t.Fatalf("score = %v, error = %v", f.score, err)
	}
	f.sent = false
	f.run.Answers = f.run.Answers[:3]
	_, err = s.Submit(context.Background(), "worker", "org", testRunID, 1, "submit-002")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || f.sent {
		t.Fatal("submitted missing required answer", err)
	}
}

func TestCreateRunClientTimestampNormalized(t *testing.T) {
	f := &fakeStore{actor: domain.Actor{ID: "worker", Role: "CHEF"}}
	s := New(fakeWork{f})
	now := time.Now().UTC().Add(-time.Minute)
	run, err := s.CreateRun(context.Background(), "worker", "org", domain.Run{ID: testRunID,
		LocationID: "40000000-0000-0000-0000-000000000001", TemplateVersionID: "30000000-0000-0000-0000-000000000001",
		ClientOperationID: "50000000-0000-0000-0000-000000000001", ClientCreatedAt: now})
	if err != nil || run.ClientCreatedAt.Nanosecond()%1000 != 0 {
		t.Fatal("timestamp not normalized", err)
	}
}

func (f *fakeStore) CatalogPage(context.Context, string, pagination.Window, string) ([]domain.Template, error) {
	return nil, nil
}

func (f *fakeStore) RunHistory(context.Context, domain.Actor, string, pagination.Window, domain.HistoryFilter) ([]domain.RunSummary, error) {
	return nil, nil
}

func TestOnlyChefPerformsDailyReviews(t *testing.T) {
	for _, role := range []string{"ADMIN", "SUPER_ADMIN", "WORKER", "CHEF"} {
		t.Run(role, func(t *testing.T) {
			f := &fakeStore{actor: domain.Actor{ID: "actor", Role: role}, run: domain.Run{ID: testRunID, OrganizationID: "org", PerformedBy: "actor", Status: "DRAFT", RowVersion: 1, Items: []domain.Item{{ID: testItemID, ResponseType: "COMPLIANCE", Required: true}}, Answers: []domain.Answer{{ItemID: testItemID, ComplianceResult: "COMPLIANT"}}}}
			service := New(fakeWork{f})
			_, createErr := service.CreateRun(context.Background(), "actor", "org", domain.Run{ID: testRunID, LocationID: "40000000-0000-0000-0000-000000000001", TemplateVersionID: "30000000-0000-0000-0000-000000000001", ClientOperationID: "50000000-0000-0000-0000-000000000001", ClientCreatedAt: time.Now().Add(-time.Minute)})
			_, saveErr := service.SaveAnswers(context.Background(), "actor", "org", testRunID, 1, "save-role-test", f.run.Answers)
			_, submitErr := service.Submit(context.Background(), "actor", "org", testRunID, 1, "submit-role-test")
			for _, err := range []error{createErr, saveErr, submitErr} {
				if role == "CHEF" && err != nil || role != "CHEF" && !errors.Is(err, domain.ErrDenied) {
					t.Fatalf("unexpected permission for %s: %v", role, err)
				}
			}
		})
	}
}

func TestReviewReadingRequiresSupervisionEvenForAnOldOwnRun(t *testing.T) {
	for _, role := range []string{"ADMIN", "SUPER_ADMIN", "WORKER", "CHEF"} {
		store := &fakeStore{actor: domain.Actor{ID: "actor", Role: role}, run: domain.Run{ID: testRunID, PerformedBy: "actor"}}
		_, err := New(fakeWork{store}).Run(context.Background(), "actor", "org", testRunID)
		if role == "WORKER" && !errors.Is(err, domain.ErrDenied) || role != "WORKER" && err != nil {
			t.Fatalf("unexpected reading permission for %s: %v", role, err)
		}
	}
}
