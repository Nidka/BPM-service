package app

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/database/postgres"
	"github.com/Nidka/BPM-service/internal/core/httpserver"
	firebaseauth "github.com/Nidka/BPM-service/internal/core/integrations/firebase"
	"github.com/Nidka/BPM-service/internal/core/integrations/r2"
	"github.com/Nidka/BPM-service/internal/features/checklists"
	"github.com/Nidka/BPM-service/internal/features/identity"
	"github.com/Nidka/BPM-service/internal/features/media"
	"github.com/Nidka/BPM-service/internal/features/operations"
	"github.com/Nidka/BPM-service/internal/features/organizations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Container es la raíz de composición. Aquí se crea la infraestructura concreta
// y se inyecta mediante constructores e interfaces pequeñas.
type Container struct {
	Database      *pgxpool.Pool
	TokenVerifier contracts.TokenVerifier
	ObjectStorage contracts.ObjectStorage
	Server        *httpserver.Server

	closeOnce sync.Once
}

func NewContainer(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	startupReporter contracts.StartupReporter,
) (*Container, error) {
	pool, err := postgres.Open(ctx, cfg.Database, logger)
	if err != nil {
		return nil, err
	}

	container := &Container{Database: pool}
	fail := func(err error) (*Container, error) {
		container.Close()
		return nil, err
	}

	if err := identity.RequireSessionSchema(ctx, pool); err != nil {
		return fail(err)
	}

	if err := media.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}

	if err := operations.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}

	if cfg.Firebase.Enabled {
		container.TokenVerifier, err = firebaseauth.New(ctx, cfg.Firebase)
		if err != nil {
			return fail(err)
		}
		logger.Info("Firebase authentication ready")
	}

	if cfg.R2.Enabled {
		container.ObjectStorage, err = r2.New(ctx, cfg.R2)
		if err != nil {
			return fail(err)
		}
		logger.Info("private R2 storage ready")
	}

	identityModule, err := identity.New(identity.Dependencies{
		Database:               container.Database,
		TokenVerifier:          container.TokenVerifier,
		Logger:                 logger,
		SessionAbsoluteTimeout: cfg.Session.AbsoluteTimeout,
		SessionIdleTimeout:     cfg.Session.IdleTimeout,
	})
	if err != nil {
		return fail(err)
	}

	router := httpserver.NewRouter(cfg, logger, httpserver.Dependencies{
		Database: container.Database,
		APIRoutes: []httpserver.APIRouteRegistrar{
			identityModule.RegisterRoutes,
			organizations.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			checklists.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			media.Routes(container.Database, container.TokenVerifier, container.ObjectStorage, cfg.R2, logger, identityModule.Sessions),
			operations.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
		},
	})
	container.Server = httpserver.New(cfg, logger, router, startupReporter)
	return container, nil
}

func (container *Container) Close() {
	container.closeOnce.Do(func() {
		if container.Database != nil {
			container.Database.Close()
		}
	})
}
