// Package platformsettings compone los ajustes de plataforma: contacto de soporte (correo y WhatsApp opcional)
// visible para toda persona activa y editable solo por el administrador general (SUPER_ADMIN).
package platformsettings

import (
	"context"
	"log/slog"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/platformsettings/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/platformsettings/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/platformsettings/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequireSchema falla de forma explícita si falta la migración V22.
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}

// Routes devuelve el registrador de rutas.
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	handler := httpadapter.New(application.New(postgres.New(pool)), logger)
	return func(r chi.Router) { handler.Register(r, middleware.Authenticate(verifier, sessions...)) }
}
