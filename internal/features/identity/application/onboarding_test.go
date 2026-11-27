package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/identity/domain"
)

type onboardingRepositoryStub struct {
	userID string
	seenAt *time.Time
	calls  int
	err    error
}

func (stub *onboardingRepositoryStub) SetOnboardingSeen(_ context.Context, userID string, seenAt *time.Time) error {
	stub.calls++
	stub.userID, stub.seenAt = userID, seenAt
	return stub.err
}

func TestSetOnboardingMarksAndClears(t *testing.T) {
	fixed := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	repo := &onboardingRepositoryStub{}
	useCase := NewSetOnboarding(repo, func() time.Time { return fixed })
	identity := contracts.Identity{UID: "uid-1", EmailVerified: true}

	seenAt, err := useCase.Execute(context.Background(), identity, true)
	if err != nil || seenAt == nil || !seenAt.Equal(fixed) || repo.userID != "uid-1" || repo.seenAt == nil {
		t.Fatalf("mark failed: %v %v %+v", seenAt, err, repo)
	}
	seenAt, err = useCase.Execute(context.Background(), identity, false)
	if err != nil || seenAt != nil || repo.seenAt != nil {
		t.Fatalf("clear failed: %v %v %+v", seenAt, err, repo)
	}
}

func TestSetOnboardingRejectsUnverifiedOrMissingIdentity(t *testing.T) {
	repo := &onboardingRepositoryStub{}
	useCase := NewSetOnboarding(repo, nil)
	if _, err := useCase.Execute(context.Background(), contracts.Identity{}, true); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("expected unauthenticated, got %v", err)
	}
	if _, err := useCase.Execute(context.Background(), contracts.Identity{UID: "uid"}, true); !errors.Is(err, domain.ErrEmailUnverified) {
		t.Fatalf("expected unverified, got %v", err)
	}
	if repo.calls != 0 {
		t.Fatal("repository must not be called")
	}
}

func TestSetOnboardingPropagatesInactiveUser(t *testing.T) {
	repo := &onboardingRepositoryStub{err: domain.ErrUserNotFound}
	_, err := NewSetOnboarding(repo, nil).Execute(context.Background(), contracts.Identity{UID: "uid", EmailVerified: true}, true)
	if !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
