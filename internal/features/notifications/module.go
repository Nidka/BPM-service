// Package notifications compone dispositivos privados y entrega externa por puertos.
package notifications

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/notifications/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/notifications/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
)

func Routes(p *pgxpool.Pool, v contracts.TokenVerifier, l *slog.Logger, s ...contracts.SessionValidator) func(chi.Router) {
	h := httpadapter.New(application.NewDevices(postgres.New(p)), l)
	return func(r chi.Router) { h.Register(r, middleware.Authenticate(v, s...)) }
}
func RequireSchema(ctx context.Context, p *pgxpool.Pool) error {
	rows, e := p.Query(ctx, `SELECT d.token_hash,n.target_key,n.target_device_id,n.request_payload,n.first_attempt_at,n.lease_token,n.locked_until,o.dedup_key,o.expires_at FROM mobile_devices d CROSS JOIN notification_deliveries n CROSS JOIN outbox_events o LIMIT 0`)
	if e != nil {
		return errors.New("notifications migration V7 is required")
	}
	rows.Close()
	return rows.Err()
}
