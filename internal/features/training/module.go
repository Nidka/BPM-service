// Package training compone la Academia BPM: cursos versionados con video,
// modo estudio, evaluación calificada en servidor y constancias.
package training

import (
	"context"
	"log/slog"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/training/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/training/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/training/application"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Límite de la verificación pública de constancias por IP: 1 solicitud por segundo con ráfaga de 10.
const (
	publicVerifyRPS   = 1.0
	publicVerifyBurst = 10
)

// RequireSchema falla de forma explícita si falta la migración V8 o V11.
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}

// Routes devuelve el registrador de rutas. videos puede ser nil cuando R2 está
// deshabilitado: la lectura de cursos funciona y las operaciones con video
// responden 503.
//
// La verificación pública de constancias (sin autenticación) usa un limitador por IP
// propio y más estricto que el global, derivado de la configuración HTTP.
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, videos contracts.VideoStorage, cfg config.R2Config, httpCfg config.HTTPConfig, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	service := application.New(postgres.New(pool), videos, domain.VideoPolicy{MaxBytes: cfg.TrainingVideoMaxBytes})
	handler := httpadapter.New(service, logger)
	publicLimiter := middleware.NewIPRateLimiter(publicVerifyRPS, publicVerifyBurst, httpCfg.TrustProxyHeaders)
	return func(r chi.Router) {
		handler.Register(r, middleware.Authenticate(verifier, sessions...))
		handler.RegisterPublic(r, publicLimiter.Middleware)
	}
}

// InstallCatalog instala (o actualiza) los cinco módulos BPM y el banco oficial de preguntas en una
// organización. Es la operación de plataforma que usa el comando cmd/catalogo-bpm; es idempotente y con
// dryRun no guarda nada. by es la persona o proceso que queda en la auditoría (un usuario existente).
func InstallCatalog(ctx context.Context, pool *pgxpool.Pool, org, by string, dryRun bool) ([]application.InstalledCourse, error) {
	service := application.New(postgres.New(pool), nil, domain.VideoPolicy{})
	return service.InstallBPMCatalog(ctx, org, by, dryRun)
}
