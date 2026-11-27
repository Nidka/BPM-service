package application

import (
	"context"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/identity/domain"
)

// SetOnboarding marca o desmarca la introducción de primeros pasos de la persona autenticada.
type SetOnboarding struct {
	repository OnboardingRepository
	now        func() time.Time
}

func NewSetOnboarding(repository OnboardingRepository, now func() time.Time) *SetOnboarding {
	if now == nil {
		now = time.Now
	}
	return &SetOnboarding{repository: repository, now: now}
}

// Execute guarda la fecha actual si seen es true o la borra si es false (para volver a verla desde Ayuda).
func (useCase *SetOnboarding) Execute(ctx context.Context, identity contracts.Identity, seen bool) (*time.Time, error) {
	if strings.TrimSpace(identity.UID) == "" {
		return nil, domain.ErrUnauthenticated
	}
	if !identity.EmailVerified {
		return nil, domain.ErrEmailUnverified
	}
	var seenAt *time.Time
	if seen {
		at := useCase.now().UTC().Truncate(time.Second)
		seenAt = &at
	}
	if err := useCase.repository.SetOnboardingSeen(ctx, identity.UID, seenAt); err != nil {
		return nil, err
	}
	return seenAt, nil
}
