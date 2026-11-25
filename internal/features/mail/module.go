// Package mail implementa el correo transaccional propio de BPM (verificación de correo y nueva contraseña) enviado por
// Resend, con respaldo al envío de Firebase en el cliente. Ver specs/MAIL.md.
package mail

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Nidka/BPM-service/internal/core/config"
	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	coreresend "github.com/Nidka/BPM-service/internal/core/integrations/resend"
	mailfirebase "github.com/Nidka/BPM-service/internal/features/mail/adapters/firebase"
	mailhttp "github.com/Nidka/BPM-service/internal/features/mail/adapters/http"
	mailresend "github.com/Nidka/BPM-service/internal/features/mail/adapters/resend"
	"github.com/Nidka/BPM-service/internal/features/mail/application"
	"github.com/go-chi/chi/v5"
)

// LinkGeneratorPort es el puerto de generación de enlaces (alias para la composición en core/app).
type LinkGeneratorPort = application.LinkGenerator

// Dependencies son las piezas que compone core/app. Links puede ser nil (Firebase deshabilitado): los envíos responden
// MAIL_UNAVAILABLE y el cliente usa Firebase.
type Dependencies struct {
	Authenticate func(http.Handler) http.Handler
	Links        application.LinkGenerator
	Mailer       application.Mailer
	AppURL       string
	TrustProxy   bool
	Logger       *slog.Logger
}

// NewLinks crea el generador de enlaces con Firebase Admin.
func NewLinks(ctx context.Context, cfg config.FirebaseConfig) (application.LinkGenerator, error) {
	links, err := mailfirebase.NewActionLinks(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return links, nil
}

// NewMailer crea el envío por Resend con el remitente y la dirección de respuesta configurados.
func NewMailer(cfg config.EmailConfig) application.Mailer {
	return mailresend.New(coreresend.New(&http.Client{Timeout: 10 * time.Second}, cfg.ResendAPIKey), cfg.From, cfg.ReplyTo)
}

// Routes devuelve el registrador de rutas para APIRoutes.
func Routes(d Dependencies) func(chi.Router) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	service := application.New(d.Links, d.Mailer, d.AppURL)
	// 5 solicitudes por IP en ráfaga, una cada 5 s de media; por cuenta, 3 en 10 min (verificación) o 3 en 15 min por correo.
	ip := middleware.NewIPRateLimiter(0.2, 5, d.TrustProxy)
	h := mailhttp.New(service, d.Authenticate, ip.Middleware,
		application.NewKeyedLimiter(3, mailhttp.VerifyWindow, nil), application.NewKeyedLimiter(3, mailhttp.ResetWindow, nil), d.Logger)
	return h.Register
}
