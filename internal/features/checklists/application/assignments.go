package application

import (
	"context"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// AssignmentStore expresa únicamente las consultas y escrituras de programación.
type AssignmentStore interface {
	Actor(context.Context, string, string) (domain.Actor, error)
	AssignmentPage(context.Context, domain.Actor, string, pagination.Window) ([]domain.Assignment, error)
	Assignment(context.Context, domain.Actor, string, string, bool) (domain.Assignment, error)
	CreateAssignment(context.Context, domain.Actor, string, domain.Assignment) (domain.Assignment, error)
	DeactivateAssignment(context.Context, domain.Actor, string, string) (domain.Assignment, error)
	PeriodRuns(context.Context, string, []domain.AssignmentPeriod) (map[string]domain.AssignedRun, error)
}

type AssignmentWork interface {
	WithinAssignments(context.Context, func(AssignmentStore) error) error
}
type Assignments struct {
	work AssignmentWork
	now  func() time.Time
}

func NewAssignments(work AssignmentWork) *Assignments { return &Assignments{work: work, now: time.Now} }

func (s *Assignments) List(ctx context.Context, uid, org string, r pagination.Request) (out pagination.Page[domain.Assignment], err error) {
	scope := []string{uid, org, "checklist-assignments"}
	w, err := pagination.Open(r, scope...)
	if err != nil {
		return out, err
	}
	err = s.work.WithinAssignments(ctx, func(db AssignmentStore) error {
		a, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanSuperviseRuns(a.Role) {
			return domain.ErrDenied
		}
		items, e := db.AssignmentPage(ctx, a, org, w)
		if e != nil {
			return e
		}
		out = pagination.Close(items, w, func(v domain.Assignment) pagination.Position {
			return pagination.Position{Key: v.TemplateName, ID: v.ID}
		}, scope...)
		return s.enrich(ctx, db, org, out.Items)
	})
	return
}

func (s *Assignments) Get(ctx context.Context, uid, org, id string) (out domain.Assignment, err error) {
	if !uuid.MatchString(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.WithinAssignments(ctx, func(db AssignmentStore) error {
		a, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanSuperviseRuns(a.Role) {
			return domain.ErrDenied
		}
		v, e := db.Assignment(ctx, a, org, id, false)
		if e != nil {
			return e
		}
		items := []domain.Assignment{v}
		if e = s.enrich(ctx, db, org, items); e != nil {
			return e
		}
		out = items[0]
		return nil
	})
	return
}

func (s *Assignments) enrich(ctx context.Context, db AssignmentStore, org string, items []domain.Assignment) error {
	periods := make([]domain.AssignmentPeriod, len(items))
	now := s.now()
	for i := range items {
		p, e := items[i].PeriodAt(now)
		if e != nil {
			return e
		}
		items[i].Period = p
		periods[i] = p
	}
	runs, err := db.PeriodRuns(ctx, org, periods)
	if err != nil {
		return err
	}
	for i := range items {
		if run, ok := runs[items[i].ID]; ok {
			items[i].Run = &run
		}
	}
	return nil
}

func (s *Assignments) Create(ctx context.Context, uid, org string, v domain.Assignment) (out domain.Assignment, err error) {
	fields := map[string]string{}
	for key, id := range map[string]string{"id": v.ID, "templateId": v.TemplateID, "locationId": v.LocationID} {
		if !uuid.MatchString(id) {
			fields[key] = "Selecciona un registro válido."
		}
	}
	switch v.Frequency {
	case "ONCE", "DAILY", "WEEKLY", "MONTHLY":
	default:
		fields["frequency"] = "Selecciona la frecuencia del control."
	}
	if v.DueTime != "" {
		parsed, e := time.Parse("15:04", v.DueTime)
		if e != nil || parsed.Format("15:04") != v.DueTime {
			fields["dueTime"] = "Usa una hora válida con formato HH:MM."
		}
	}
	if len(fields) > 0 {
		return out, &domain.ValidationError{Fields: fields}
	}
	v.ID, v.TemplateID, v.LocationID = strings.ToLower(v.ID), strings.ToLower(v.TemplateID), strings.ToLower(v.LocationID)
	err = s.work.WithinAssignments(ctx, func(db AssignmentStore) error {
		a, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanManageTemplates(a.Role) {
			return domain.ErrDenied
		}
		out, e = db.CreateAssignment(ctx, a, org, v)
		return e
	})
	return
}

func (s *Assignments) Deactivate(ctx context.Context, uid, org, id string) (out domain.Assignment, err error) {
	if !uuid.MatchString(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.WithinAssignments(ctx, func(db AssignmentStore) error {
		a, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanManageTemplates(a.Role) {
			return domain.ErrDenied
		}
		out, e = db.DeactivateAssignment(ctx, a, org, id)
		return e
	})
	return
}
