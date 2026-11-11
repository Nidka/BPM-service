package app

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/database/postgres"
	firebaseauth "github.com/Nidka/BPM-service/internal/core/integrations/firebase"
	"github.com/Nidka/BPM-service/internal/core/observability"
	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/alerts"
	"github.com/Nidka/BPM-service/internal/features/notifications"
	"github.com/Nidka/BPM-service/internal/features/notifications/adapters/fcm"
	notificationpg "github.com/Nidka/BPM-service/internal/features/notifications/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/notifications/adapters/resend"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	notificationdomain "github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"github.com/Nidka/BPM-service/internal/features/reports"
	"net/http"
	"time"
)

// RunWorker procesa únicamente eventos registrados; no aloja API ni crea revisiones.
func RunWorker(ctx context.Context) error {
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	logger := observability.DefaultLogger(cfg.App)
	pool, e := postgres.Open(ctx, cfg.Database, logger)
	if e != nil {
		return e
	}
	defer pool.Close()
	if e = alerts.RequireSchema(ctx, pool); e != nil {
		return e
	}
	if e = notifications.RequireSchema(ctx, pool); e != nil {
		return e
	}
	if e = reports.RequireSchema(ctx, pool); e != nil {
		return e
	}
	providers := map[string]application.Provider{}
	if cfg.Email.ResendAPIKey != "" && cfg.Email.From != "" {
		providers["EMAIL"] = resend.New(&http.Client{Timeout: 12 * time.Second}, cfg.Email.ResendAPIKey)
	}
	if cfg.Worker.FCMEnabled {
		client, e := firebaseauth.NewMessaging(ctx, cfg.Firebase)
		if e != nil {
			return e
		}
		providers["FCM"] = fcm.New(client)
	}
	var weeklyMailer reports.Mailer
	if providers["EMAIL"] != nil {
		weeklyMailer = weeklyReportMailer{provider: providers["EMAIL"], from: cfg.Email.From}
	}
	weekly := reports.NewWeeklyJob(pool, weeklyMailer)
	dispatcher := application.NewDispatcher(notificationpg.New(pool), providers, cfg.Email.From)
	evaluator := alerts.New(pool, logger)
	runner := outbox.NewRunner(outbox.NewPostgres(pool), map[string]outbox.Handler{
		"operations.receipt.recorded.v1": func(ctx context.Context, v outbox.Event) error {
			return evaluator.EvaluateReceipt(ctx, v.OrganizationID, v.AggregateID, time.Now().UTC())
		},
		"notifications.alert.ready.v1": func(ctx context.Context, v outbox.Event) error {
			e := dispatcher.Dispatch(ctx, v.OrganizationID, v.AggregateID, time.Now().UTC())
			if errors.Is(e, application.ErrUnavailable) {
				return outbox.ErrUnavailable
			}
			return e
		},
	}, outbox.Options{})
	ticker := time.NewTicker(cfg.Worker.Interval)
	defer ticker.Stop()
	logger.Info("notification worker ready", "emailConfigured", providers["EMAIL"] != nil, "pushConfigured", providers["FCM"] != nil)
	tick := func() {
		run, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		now := time.Now().UTC()
		if e := evaluator.EvaluateScheduled(run, now); e != nil {
			logger.Error("alert evaluation failed", "code", "EVALUATION_FAILED")
		}
		if e := runner.Tick(run); e != nil {
			logger.Error("outbox processing failed", "code", "OUTBOX_FAILED")
		}
		if e := dispatcher.SendDue(run, time.Now().UTC()); e != nil {
			logger.Error("notification delivery failed", "code", "DELIVERY_FAILED")
		}
		if e := weekly.Stage(run, time.Now().UTC()); e != nil {
			logger.Error("weekly report scheduling failed", "code", "WEEKLY_SCHEDULE_FAILED")
		}
		if e := weekly.SendDue(run, 25); e != nil {
			logger.Error("weekly report delivery failed", "code", "WEEKLY_DELIVERY_FAILED")
		}
	}
	tick()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			tick()
		}
	}
}

// weeklyReportMailer adapta el proveedor de correo de notificaciones al puerto del reporte semanal.
type weeklyReportMailer struct {
	provider application.Provider
	from     string
}

func (m weeklyReportMailer) Send(ctx context.Context, key, to, subject, body string) (string, error) {
	id, e := m.provider.Send(ctx, key, notificationdomain.Message{From: m.from, To: to, Subject: subject, Text: body})
	if errors.Is(e, application.ErrPermanent) {
		return "", reports.ErrMailPermanent
	}
	return id, e
}
