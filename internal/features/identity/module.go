package identity

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	httpmiddleware "github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	identityhttp "github.com/Nidka/BPM-service/internal/features/identity/adapters/http"
	identitypostgres "github.com/Nidka/BPM-service/internal/features/identity/adapters/postgres"
	"github.com/Nidka/BPM-service/internal/features/identity/application"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequireSessionSchema impide atender tráfico si falta la migración V2.
func RequireSessionSchema(ctx context.Context, pool *pgxpool.Pool) error {
	return identitypostgres.NewRepository(pool).RequireSessionSchema(ctx)
}

type Dependencies struct {
	Database               *pgxpool.Pool
	TokenVerifier          contracts.TokenVerifier
	Logger                 *slog.Logger
	SessionAbsoluteTimeout time.Duration
	SessionIdleTimeout     time.Duration
}

type Module struct {
	routes   *identityhttp.Routes
	Sessions contracts.SessionValidator
}

func New(dependencies Dependencies) (*Module, error) {
	if dependencies.Database == nil {
		return nil, errors.New("identity database is required")
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.Default()
	}

	repository := identitypostgres.NewRepository(dependencies.Database)
	absolute, idle := dependencies.SessionAbsoluteTimeout, dependencies.SessionIdleTimeout
	if absolute == 0 {
		absolute = 8 * time.Hour
	}
	if idle == 0 {
		idle = 15 * time.Minute
	}
	sessions := application.NewSessions(repository, absolute, idle, time.Now)
	getCurrentProfile := application.NewGetCurrentProfile(repository)
	handler := identityhttp.NewHandler(getCurrentProfile, dependencies.Logger).WithOnboarding(application.NewSetOnboarding(repository, time.Now))
	registerHandler := identityhttp.NewRegisterHandler(application.NewRegister(repository), dependencies.Logger)
	registrationLimit := httpmiddleware.NewIPRateLimiter(0.2, 5, false)
	return &Module{
		Sessions: sessions,
		routes:   identityhttp.NewRoutes(handler, httpmiddleware.Authenticate(dependencies.TokenVerifier, sessions)).WithRegistration(registerHandler, registrationLimit.Middleware).WithSessions(sessions),
	}, nil
}

func (module *Module) RegisterRoutes(router chi.Router) {
	module.routes.Register(router)
}
