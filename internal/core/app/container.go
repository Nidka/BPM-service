package app

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/core/database/postgres"
	"github.com/Nidka/BPM-service/internal/core/httpserver"
	httpmiddleware "github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	firebaseauth "github.com/Nidka/BPM-service/internal/core/integrations/firebase"
	"github.com/Nidka/BPM-service/internal/core/integrations/r2"
	"github.com/Nidka/BPM-service/internal/features/alerts"
	"github.com/Nidka/BPM-service/internal/features/checklists"
	"github.com/Nidka/BPM-service/internal/features/identity"
	"github.com/Nidka/BPM-service/internal/features/incidents"
	"github.com/Nidka/BPM-service/internal/features/mail"
	"github.com/Nidka/BPM-service/internal/features/media"
	"github.com/Nidka/BPM-service/internal/features/normative"
	"github.com/Nidka/BPM-service/internal/features/notifications"
	"github.com/Nidka/BPM-service/internal/features/operations"
	"github.com/Nidka/BPM-service/internal/features/organizations"
	"github.com/Nidka/BPM-service/internal/features/reports"
	"github.com/Nidka/BPM-service/internal/features/research"
	"github.com/Nidka/BPM-service/internal/features/training"
	"github.com/Nidka/BPM-service/internal/features/usage"
	"github.com/Nidka/BPM-service/internal/features/webhooks"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Container es la raíz de composición. Aquí se crea la infraestructura concreta
// y se inyecta mediante constructores e interfaces pequeñas.
type Container struct {
	Database      *pgxpool.Pool
	TokenVerifier contracts.TokenVerifier
	ObjectStorage contracts.ObjectStorage
	VideoStorage  contracts.VideoStorage
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

	if err := incidents.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}

	if err := training.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}

	if err := organizations.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}

	if err := research.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}

	if err := alerts.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}
	if err := notifications.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}
	if err := reports.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}
	if err := usage.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}
	if err := normative.RequireSchema(ctx, pool); err != nil {
		return fail(err)
	}
	if err := webhooks.RequireSchema(ctx, pool); err != nil {
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
		storage, err := r2.New(ctx, cfg.R2)
		if err != nil {
			return fail(err)
		}
		container.ObjectStorage = storage
		container.VideoStorage = storage
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

	// Correo transaccional propio. Sin RESEND_API_KEY (o sin Firebase Admin) las rutas responden MAIL_UNAVAILABLE y el cliente
	// usa el envío de Firebase; el arranque nunca depende de este proveedor.
	var mailLinks mail.LinkGeneratorPort
	if cfg.Firebase.Enabled {
		links, linksErr := mail.NewLinks(ctx, cfg.Firebase)
		if linksErr != nil {
			logger.Warn("transactional mail links unavailable; clients will use the Firebase fallback")
		} else {
			mailLinks = links
		}
	}
	mailRoutes := mail.Routes(mail.Dependencies{
		Authenticate: httpmiddleware.Authenticate(container.TokenVerifier, identityModule.Sessions),
		Links:        mailLinks,
		Mailer:       mail.NewMailer(cfg.Email),
		AppURL:       cfg.Email.AppPublicURL,
		TrustProxy:   cfg.HTTP.TrustProxyHeaders,
		Logger:       logger,
	})

	router := httpserver.NewRouter(cfg, logger, httpserver.Dependencies{
		Database: container.Database,
		APIRoutes: []httpserver.APIRouteRegistrar{
			identityModule.RegisterRoutes,
			mailRoutes,
			alerts.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			notifications.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			organizations.RoutesWithSeeder(container.Database, container.TokenVerifier, logger, cfg.Email.InvitationAcceptURL, cfg.Email.ResendAPIKey != "" && cfg.Email.From != "", cfg.HTTP.TrustProxyHeaders, training.NewOrganizationSeeder(container.Database), identityModule.Sessions),
			checklists.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			media.Routes(container.Database, container.TokenVerifier, container.ObjectStorage, cfg.R2, logger, identityModule.Sessions),
			operations.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			incidents.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			research.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			training.Routes(container.Database, container.TokenVerifier, container.VideoStorage, cfg.R2, cfg.HTTP, logger, identityModule.Sessions),
			reports.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			usage.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			normative.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
			webhooks.Routes(container.Database, container.TokenVerifier, logger, identityModule.Sessions),
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
