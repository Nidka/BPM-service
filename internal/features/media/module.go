// Package media compone los adaptadores de evidencias privadas.
package media

import (
	"context"
	"log/slog"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/media/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/media/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/media/application"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}

func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, objects contracts.ObjectStorage, cfg config.R2Config, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	allowed := make([]string, 0, 2)
	for _, mime := range []string{"image/jpeg", "image/png"} {
		if _, ok := cfg.AllowedMIMETypes[mime]; ok {
			allowed = append(allowed, mime)
		}
	}
	h := httpadapter.New(application.New(postgres.New(pool), objects, domain.Policy{MaxBytes: cfg.MaxUploadBytes, MaxFiles: 3, AllowedMIMEs: allowed}), logger)
	return func(r chi.Router) { h.Register(r, middleware.Authenticate(verifier, sessions...)) }
}
