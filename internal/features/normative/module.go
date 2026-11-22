// Package normative compone el agente normativo (RF-46/47): normativa cargada por el administrador
// general y consultas respondidas por Claude solo con esos documentos.
package normative

import (
	"context"
	"log/slog"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/features/normative/adapters/claude"
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

// Routes devuelve el registrador de rutas. Sin ANTHROPIC_API_KEY el agente responde 503 (apagado).
func Routes(pool *pgxpool.Pool, cfg config.AssistantConfig, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	var answerer application.Answerer
	if cfg.APIKey != "" {
		answerer = claude.New(cfg.APIKey, cfg.Model)
	}
	handler := httpadapter.New(application.New(postgres.New(pool), answerer, postgres.NewID), logger)
	return func(r chi.Router) { handler.Register(r, middleware.Authenticate(verifier, sessions...)) }
}
