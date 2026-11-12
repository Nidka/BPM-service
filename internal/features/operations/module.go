// Package operations compone recepción, consulta PEPS y tareas de limpieza.
package operations

import (
	"context"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/operations/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/operations/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/operations/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
)

// RequireSchema comprueba V5 (operación diaria) y V13 (turnos y movimientos de stock).
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if e := postgres.New(pool).RequireSchema(ctx); e != nil {
		return e
	}
	return postgres.New(pool).RequireSchemaPlus(ctx)
}
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	h := httpadapter.New(application.New(postgres.New(pool)), logger)
	return func(r chi.Router) { h.Register(r, middleware.Authenticate(verifier, sessions...)) }
}
