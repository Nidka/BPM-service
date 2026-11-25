// Package firebase genera los enlaces de acción de correo con Firebase Admin SDK.
package firebase

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	firebasesdk "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"github.com/Nidka/BPM-service/internal/core/config"
	mailapp "github.com/Nidka/BPM-service/internal/features/mail/application"
	"google.golang.org/api/option"
)

// actionLinkClient es la parte del cliente de Firebase Auth que genera enlaces de acción.
type actionLinkClient interface {
	EmailVerificationLink(context.Context, string) (string, error)
	PasswordResetLink(context.Context, string) (string, error)
}

// ActionLinks genera con Firebase Admin los enlaces de verificación y de nueva contraseña. El enlace se reescribe después
// hacia la página propia de la app; Firebase no envía ningún correo en este camino.
type ActionLinks struct{ client actionLinkClient }

// NewActionLinks conserva la credencial administrativa solo en el servidor.
func NewActionLinks(ctx context.Context, cfg config.FirebaseConfig) (*ActionLinks, error) {
	raw, err := base64.StdEncoding.DecodeString(cfg.ServiceAccountJSONBase64)
	if err != nil {
		return nil, errors.New("credencial Firebase inválida")
	}
	app, err := firebasesdk.NewApp(ctx, &firebasesdk.Config{ProjectID: cfg.ProjectID}, option.WithCredentialsJSON(raw))
	if err != nil {
		return nil, fmt.Errorf("initialize Firebase: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize Firebase Auth: %w", err)
	}
	return &ActionLinks{client: client}, nil
}

func (a *ActionLinks) VerificationLink(ctx context.Context, email string) (string, error) {
	link, err := a.client.EmailVerificationLink(ctx, email)
	return link, translateLinkError(err)
}

func (a *ActionLinks) PasswordResetLink(ctx context.Context, email string) (string, error) {
	link, err := a.client.PasswordResetLink(ctx, email)
	return link, translateLinkError(err)
}

// translateLinkError no conserva el error original: puede incluir la dirección de correo.
func translateLinkError(err error) error {
	switch {
	case err == nil:
		return nil
	case auth.IsEmailNotFound(err) || auth.IsUserNotFound(err):
		return mailapp.ErrAccountNotFound
	default:
		return errors.New("no se pudo generar el enlace de acción")
	}
}

var _ mailapp.LinkGenerator = (*ActionLinks)(nil)
