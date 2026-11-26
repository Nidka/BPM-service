// Package reports compone los reportes BPM: cumplimiento por día/sede/empresa, exportación CSV y PDF,
// KPIs, avance de capacitación, constancias de cumplimiento y el correo semanal.
package reports

import (
	"context"
	"log/slog"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/reports/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/reports/adapters/pdf"
	"github.com/Nidka/BPM-service/internal/features/reports/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/reports/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Mailer es el puerto de correo del trabajo semanal; el worker lo implementa con su proveedor.
type Mailer = application.Mailer

// ErrMailPermanent es el error que debe devolver un Mailer ante un rechazo definitivo.
var ErrMailPermanent = application.ErrMailPermanent

// RequireSchema falla de forma explícita si falta la migración V10.
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}

// Routes devuelve el registrador de rutas de reportes.
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	repository := postgres.New(pool)
	handler := httpadapter.New(application.New(repository, repository), logger).WithPDF(pdf.Render)
	return func(r chi.Router) { handler.Register(r, middleware.Authenticate(verifier, sessions...)) }
}

// WeeklyJob es el trabajo del worker que programa y envía el reporte semanal (RF-29).
type WeeklyJob = application.WeeklyJob

// NewWeeklyJob crea el trabajo semanal; mailer nulo desactiva la programación.
func NewWeeklyJob(pool *pgxpool.Pool, mailer Mailer) *WeeklyJob {
	return application.NewWeeklyJob(postgres.New(pool), mailer)
}
