// Package incidents compone el catálogo versionado y la atención trazable de incidencias.
package incidents

import (
	"context"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/incidents/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/incidents/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/incidents/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
)

func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	h := httpadapter.New(application.New(postgres.New(pool)), logger)
	return func(r chi.Router) { h.Register(r, middleware.Authenticate(verifier, sessions...)) }
}
