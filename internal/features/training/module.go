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

// RequireSchema falla de forma explícita si falta la migración V8.
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}

// Routes devuelve el registrador de rutas. videos puede ser nil cuando R2 está
// deshabilitado: la lectura de cursos funciona y las operaciones con video
// responden 503.
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, videos contracts.VideoStorage, cfg config.R2Config, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	service := application.New(postgres.New(pool), videos, domain.VideoPolicy{MaxBytes: cfg.TrainingVideoMaxBytes})
	handler := httpadapter.New(service, logger)
	return func(r chi.Router) { handler.Register(r, middleware.Authenticate(verifier, sessions...)) }
}
