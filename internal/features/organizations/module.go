package organizations

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	httpadapter "github.com/Nidka/BPM-service/internal/features/organizations/adapters/http"
	"github.com/Nidka/BPM-service/internal/features/organizations/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/organizations/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Routes(pool *pgxpool.Pool, verifier contracts.TokenVerifier, logger *slog.Logger, sessions ...contracts.SessionValidator) func(chi.Router) {
	h := httpadapter.New(application.New(postgres.New(pool)), logger)
	return func(r chi.Router) { h.Register(r, middleware.Authenticate(verifier, sessions...)) }
}

// RequireSchema falla de forma explícita si falta la migración V12.
func RequireSchema(ctx context.Context, pool *pgxpool.Pool) error {
	rows, e := pool.Query(ctx, `SELECT o.row_version,l.row_version,m.row_version,i.token_hash,i.location_ids,i.send_count,i.last_sent_at FROM organizations o CROSS JOIN locations l CROSS JOIN organization_memberships m CROSS JOIN organization_invitations i LIMIT 0`)
	if e != nil {
		return errors.New("accounts migration V12 is required")
	}
	rows.Close()
	return rows.Err()
}

// InvitationEvent es el tipo de evento del outbox que el worker debe registrar.
const InvitationEvent = postgres.InvitationEvent

// InvitationMailer compone el envío de invitaciones para el worker (sender puede ser nil).
func InvitationMailer(pool *pgxpool.Pool, sender application.MailSender, from, baseURL string) *application.InvitationMailer {
	return application.NewInvitationMailer(postgres.NewOutbox(pool), sender, from, baseURL)
}
