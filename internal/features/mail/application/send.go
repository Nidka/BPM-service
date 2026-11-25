// Package application contiene los casos de uso de correo transaccional: verificación de correo y recuperación de contraseña.
//
// Contrato de respaldo: si el correo propio no puede enviarse (sin RESEND_API_KEY, Resend caído o Firebase Admin no disponible)
// los casos de uso devuelven ErrUnavailable y el cliente usa el envío de Firebase (sendOobCode). Nunca se registran enlaces,
// códigos, cuerpos ni direcciones.
package application

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/Nidka/BPM-service/internal/features/mail/domain"
)

var (
	// ErrUnavailable indica que el correo no se envió y que el cliente debe usar su alternativa (Firebase).
	ErrUnavailable = errors.New("correo no disponible")
	// ErrAccountNotFound lo devuelve LinkGenerator cuando no existe una cuenta con ese correo.
	ErrAccountNotFound = errors.New("cuenta inexistente")
	// ErrInvalidEmail indica un correo con formato inválido.
	ErrInvalidEmail = errors.New("correo inválido")
)

// LinkGenerator genera el enlace de acción de Firebase (Admin SDK). Devuelve ErrAccountNotFound si el correo no existe.
type LinkGenerator interface {
	VerificationLink(ctx context.Context, email string) (string, error)
	PasswordResetLink(ctx context.Context, email string) (string, error)
}

// Mailer entrega un correo ya renderizado. Devuelve ErrUnavailable si no hay proveedor o falla el envío.
type Mailer interface {
	Configured() bool
	Send(ctx context.Context, to string, message domain.Message) error
}

// Service orquesta enlace + plantilla + envío.
type Service struct {
	links  LinkGenerator
	mailer Mailer
	appURL string
}

// New crea el servicio. links o mailer pueden ser nil: todo envío devolverá ErrUnavailable.
func New(links LinkGenerator, mailer Mailer, appURL string) *Service {
	return &Service{links: links, mailer: mailer, appURL: strings.TrimRight(appURL, "/")}
}

// Available indica si hay proveedor de correo y generador de enlaces.
func (s *Service) Available() bool {
	return s.links != nil && s.mailer != nil && s.mailer.Configured()
}

// SendVerification envía el correo de verificación a la dirección de la propia identidad autenticada.
func (s *Service) SendVerification(ctx context.Context, email string) error {
	email, err := normalize(email)
	if err != nil {
		return err
	}
	if !s.Available() {
		return ErrUnavailable
	}
	generated, err := s.links.VerificationLink(ctx, email)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			return nil
		}
		return ErrUnavailable
	}
	link, err := domain.ActionLink(generated, s.appURL, domain.ModeVerifyEmail)
	if err != nil {
		return ErrUnavailable
	}
	return s.send(ctx, email, domain.VerifyEmail(domain.ActionData{AppURL: s.appURL, Link: link}))
}

// SendPasswordReset envía el correo de nueva contraseña. Si la cuenta no existe no envía nada y devuelve nil, para que la
// respuesta HTTP sea idéntica y no revele qué correos están registrados.
func (s *Service) SendPasswordReset(ctx context.Context, email string) error {
	email, err := normalize(email)
	if err != nil {
		return err
	}
	if !s.Available() {
		return ErrUnavailable
	}
	generated, err := s.links.PasswordResetLink(ctx, email)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			return nil
		}
		return ErrUnavailable
	}
	link, err := domain.ActionLink(generated, s.appURL, domain.ModeResetPassword)
	if err != nil {
		return ErrUnavailable
	}
	return s.send(ctx, email, domain.PasswordReset(domain.ActionData{AppURL: s.appURL, Link: link}))
}

func (s *Service) send(ctx context.Context, to string, message domain.Message) error {
	if err := s.mailer.Send(ctx, to, message); err != nil {
		return ErrUnavailable
	}
	return nil
}

// NormalizeEmail valida y pasa a minúsculas una dirección simple («nombre@dominio»), sin nombre visible ni saltos de línea.
func NormalizeEmail(raw string) (string, error) { return normalize(raw) }

func normalize(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n<>, ") {
		return "", ErrInvalidEmail
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || parsed.Name != "" {
		return "", ErrInvalidEmail
	}
	return value, nil
}
