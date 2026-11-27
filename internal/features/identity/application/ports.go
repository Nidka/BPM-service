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
}
