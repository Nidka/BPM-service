// Package mailhttp expone los endpoints de correo transaccional de identidad.
package mailhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/core/httpserver/middleware"
	"github.com/Nidka/BPM-service/internal/core/httpserver/request"
	"github.com/Nidka/BPM-service/internal/core/httpserver/response"
	"github.com/Nidka/BPM-service/internal/features/mail/application"
	"github.com/go-chi/chi/v5"
)

// CodeMailUnavailable es el código estable que indica al cliente que use el envío de Firebase.
const CodeMailUnavailable = "MAIL_UNAVAILABLE"

// Sender es el caso de uso que consumen los handlers.
type Sender interface {
	SendVerification(ctx context.Context, email string) error
	SendPasswordReset(ctx context.Context, email string) error
}

// Limiter limita por clave (UID o correo).
type Limiter interface{ Allow(key string) bool }

// Handler atiende POST /auth/email/verification y POST /auth/email/password-reset.
type Handler struct {
	sender        Sender
	verifyLimit   Limiter
	resetLimit    Limiter
	logger        *slog.Logger
	authenticate  func(http.Handler) http.Handler
	ipRateLimiter func(http.Handler) http.Handler
}

// New crea el handler. authenticate protege la verificación; ipLimit se aplica a ambas rutas antes que cualquier otra
// comprobación.
func New(sender Sender, authenticate, ipLimit func(http.Handler) http.Handler, verifyLimit, resetLimit Limiter, logger *slog.Logger) *Handler {
	return &Handler{sender: sender, authenticate: authenticate, ipRateLimiter: ipLimit, verifyLimit: verifyLimit, resetLimit: resetLimit, logger: logger}
}

// Register monta las rutas bajo el prefijo de la API.
func (h *Handler) Register(r chi.Router) {
	r.With(h.ipRateLimiter, h.authenticate).Post("/auth/email/verification", h.Verification)
	r.With(h.ipRateLimiter).Post("/auth/email/password-reset", h.PasswordReset)
}

func status(w http.ResponseWriter, code int, value string) {
	response.JSON(w, code, map[string]any{"data": map[string]string{"status": value}})
}

func rateLimited(w http.ResponseWriter, requestID string) {
	w.Header().Set("Retry-After", "60")
	response.Error(w, http.StatusTooManyRequests, requestID, response.APIError{Code: "RATE_LIMITED", Message: "Se superó el límite de solicitudes. Inténtalo más tarde."})
}

// Verification envía el correo de verificación a la dirección de la identidad autenticada; el cliente no elige destinatario.
func (h *Handler) Verification(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.RequestIDFromContext(r.Context())
	identity, ok := middleware.IdentityFromContext(r.Context())
	if !ok {
		response.Unauthenticated(w, requestID)
		return
	}
	if identity.EmailVerified {
		status(w, http.StatusAccepted, "ALREADY_VERIFIED")
		return
	}
	if strings.TrimSpace(identity.Email) == "" {
		response.ValidationError(w, requestID, map[string]string{"email": "La cuenta no tiene un correo."})
		return
	}
	if !h.verifyLimit.Allow(identity.UID) {
		rateLimited(w, requestID)
		return
	}
	h.finish(w, r, h.sender.SendVerification(r.Context(), identity.Email), "SENT")
}

// PasswordReset es público y responde igual exista o no la cuenta (202), para no revelar qué correos están registrados.
func (h *Handler) PasswordReset(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.RequestIDFromContext(r.Context())
	var input struct {
		Email string `json:"email"`
	}
	if !request.DecodeStrictJSON(w, r, &input, 2048) {
		return
	}
	email, err := application.NormalizeEmail(input.Email)
	if err != nil {
		response.ValidationError(w, requestID, map[string]string{"email": "Revisa el correo electrónico."})
		return
	}
	if !h.resetLimit.Allow(email) {
		rateLimited(w, requestID)
		return
	}
	h.finish(w, r, h.sender.SendPasswordReset(r.Context(), email), "ACCEPTED")
}

func (h *Handler) finish(w http.ResponseWriter, r *http.Request, err error, ok string) {
	switch {
	case err == nil:
		status(w, http.StatusAccepted, ok)
	case errors.Is(err, application.ErrInvalidEmail):
		response.ValidationError(w, middleware.RequestIDFromContext(r.Context()), map[string]string{"email": "Revisa el correo electrónico."})
	default:
		// Solo el código; nunca el error del proveedor, que podría contener la dirección.
		h.logger.Warn("transactional mail unavailable", "request_id", middleware.RequestIDFromContext(r.Context()), "code", CodeMailUnavailable)
		w.Header().Set("Retry-After", "30")
		response.Error(w, http.StatusServiceUnavailable, middleware.RequestIDFromContext(r.Context()), response.APIError{
			Code: CodeMailUnavailable, Message: "No pudimos enviar el correo desde BPM. Usa el envío alternativo.",
		})
	}
}

// Ventanas por defecto; ver module.go.
const (
	VerifyWindow = 10 * time.Minute
	ResetWindow  = 15 * time.Minute
)
