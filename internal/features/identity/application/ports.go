package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/identity/domain"
)

type ProfileRepository interface {
	FindByUserID(ctx context.Context, userID string) (domain.Profile, error)
}

// OnboardingRepository guarda si la persona ya vio la introducción de primeros pasos.
type OnboardingRepository interface {
	SetOnboardingSeen(ctx context.Context, userID string, seenAt *time.Time) error
	// SetWhatsApp guarda (o borra con "") el WhatsApp de la persona activa, solo dígitos con código de país.
	SetWhatsApp(ctx context.Context, userID, digits string) error
}
