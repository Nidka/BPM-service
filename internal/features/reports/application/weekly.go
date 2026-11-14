package application

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
)

// ErrMailPermanent indica rechazo definitivo del proveedor de correo; el adaptador del worker
// traduce a este error el rechazo de su proveedor.
var ErrMailPermanent = errors.New("el proveedor rechazó el correo")

// Mailer envía un correo de texto. key es una clave de idempotencia estable por entrega.
type Mailer interface {
	Send(ctx context.Context, key, to, subject, body string) (providerID string, err error)
}

// Parámetros del reintento del correo semanal.
const (
	weeklyLease       = 2 * time.Minute
	weeklyMaxAttempts = 6
)

// WeeklyJob programa y envía el reporte semanal a los ADMIN (RF-29). Es seguro ejecutarlo en
// cada ciclo del worker: la unicidad (empresa, semana, destinatario) lo hace idempotente.
type WeeklyJob struct {
	work   UnitOfWork
	mailer Mailer
	now    func() time.Time
}

// NewWeeklyJob crea el trabajo. mailer nulo significa que no hay proveedor de correo: no se programa nada.
func NewWeeklyJob(work UnitOfWork, mailer Mailer) *WeeklyJob {
	return &WeeklyJob{work: work, mailer: mailer, now: time.Now}
}

// Stage programa las entregas pendientes de la semana correspondiente a now.
func (j *WeeklyJob) Stage(ctx context.Context, now time.Time) error {
	if j.mailer == nil {
		return nil
	}
	week := domain.WeekToReport(now)
	var orgs []string
	if err := j.work.Within(ctx, func(db Store) error {
		var err error
		orgs, err = db.WeeklyOrganizations(ctx, week.Start)
		return err
	}); err != nil {
		return err
	}
	var firstErr error
	for _, org := range orgs {
		if err := j.stageOrganization(ctx, org, week); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (j *WeeklyJob) stageOrganization(ctx context.Context, org string, week domain.Week) error {
	return j.work.Within(ctx, func(db Store) error {
		users, err := db.WeeklyRecipients(ctx, org)
		if err != nil || len(users) == 0 {
			return err
		}
		name, err := db.OrganizationName(ctx, org)
		if err != nil {
			return err
		}
		summary, err := buildSummary(ctx, db, domain.Scope{OrganizationID: org, AllLocations: true, Period: week.Period()})
		if err != nil {
			return err
		}
		subject, body := domain.RenderWeeklyEmail(name, week, summary)
		return db.StageWeekly(ctx, org, week.Start, users, subject, body)
	})
}

// SendDue envía las entregas vencidas (hasta limit). Cada correo se revalida antes de enviarse.
func (j *WeeklyJob) SendDue(ctx context.Context, limit int) error {
	if j.mailer == nil {
		return nil
	}
	for range limit {
		var d *WeeklyDelivery
		if err := j.work.Within(ctx, func(db Store) error {
			var err error
			d, err = db.ClaimWeekly(ctx, j.now().UTC(), weeklyLease)
			return err
		}); err != nil {
			return err
		}
		if d == nil {
			return nil
		}
		if err := j.deliver(ctx, *d); err != nil {
			return err
		}
	}
	return nil
}

func (j *WeeklyJob) deliver(ctx context.Context, d WeeklyDelivery) error {
	var to string
	if err := j.work.Within(ctx, func(db Store) error {
		var err error
		to, err = db.WeeklyRecipientEmail(ctx, d.OrganizationID, d.RecipientUserID)
		return err
	}); err != nil {
		return err
	}
	out := WeeklyOutcome{Status: "CANCELLED", Code: "RECIPIENT_UNAVAILABLE"}
	var retryAt time.Time
	if to != "" {
		call, cancel := context.WithTimeout(ctx, 15*time.Second)
		id, err := j.mailer.Send(call, "bpm-weekly/"+d.ID, to, d.Subject, d.Body)
		cancel()
		switch {
		case err == nil:
			out = WeeklyOutcome{Status: "SENT", ProviderID: id}
		case errors.Is(err, ErrMailPermanent):
			out = WeeklyOutcome{Status: "FAILED", Code: "PROVIDER_REJECTED"}
		case d.Attempts >= weeklyMaxAttempts:
			out = WeeklyOutcome{Status: "FAILED", Code: "PROVIDER_UNAVAILABLE"}
		default:
			out = WeeklyOutcome{Status: "PENDING", Code: "PROVIDER_UNAVAILABLE"}
			delay := time.Minute << min(d.Attempts, 6)
			retryAt = j.now().UTC().Add(min(delay, 6*time.Hour))
		}
	}
	return j.work.Within(ctx, func(db Store) error {
		return db.FinishWeekly(ctx, d, out, j.now().UTC(), retryAt)
	})
}
