// Package normative compone el buscador de normativa (RF-46/47): normativa cargada por el administrador
// general y búsqueda de texto completo en español para cualquier persona activa. Sin servicios externos.
package normative

import (
	"context"
	"log/slog"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/normative/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/normative/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/normative/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequireSchema falla de forma explícita si falta la migración V17.
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}

// Routes devuelve el registrador de rutas.
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	handler := httpadapter.New(application.New(postgres.New(pool), postgres.NewID), logger)
	return func(r chi.Router) { handler.Register(r, middleware.Authenticate(verifier, sessions...)) }
}
