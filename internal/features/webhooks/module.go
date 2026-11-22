// Package webhooks compone los webhooks salientes (RF-48): gestión por el ADMIN y entrega por el worker.
package webhooks

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/outbox"
	httpadapter "github.com/Nidka/BPM-service/internal/features/webhooks/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/webhooks/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/webhooks/adapters/sender"
	"github.com/Nidka/BPM-service/internal/features/webhooks/application"
	"github.com/Nidka/BPM-service/internal/features/webhooks/domain"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequireSchema falla de forma explícita si falta la migración V18.
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return postgres.New(pool).RequireSchema(ctx)
}

func service(pool *pgxpool.Pool) *application.Service {
	return application.New(postgres.New(pool), sender.New(), postgres.NewID)
}

// Routes devuelve el registrador de rutas de gestión.
func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	handler := httpadapter.New(service(pool), logger)
	return func(r chi.Router) { handler.Register(r, middleware.Authenticate(verifier, sessions...)) }
}

// AlertHandler entrega en el worker los eventos webhooks.alert.v1 que encolan las alertas.
func AlertHandler(pool *pgxpool.Pool) outbox.Handler {
	svc := service(pool)
	return func(ctx context.Context, v outbox.Event) error {
		var delivery domain.Delivery
		if err := json.Unmarshal(v.Payload, &delivery); err != nil {
			return err
		}
		delivery.OrganizationID = v.OrganizationID
		return svc.Deliver(ctx, delivery)
	}
}
