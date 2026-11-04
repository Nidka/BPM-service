// Package alerts compone reglas, evaluación y bandeja privada de avisos.
package alerts

import (
	"context"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/alerts/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/alerts/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/alerts/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
)

func New(pool *pgxpool.Pool, logger *slog.Logger) *application.Service {
	return application.New(postgres.New(pool))
}
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	h := httpadapter.New(New(pool, logger), logger)
	return func(r chi.Router) { h.Register(r, middleware.Authenticate(verifier, sessions...)) }
}
