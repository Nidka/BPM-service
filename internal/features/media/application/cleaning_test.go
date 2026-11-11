package application

import (
	"context"

	"github.com/Nidka/BPM-service/internal/features/media/domain"
)

func (f *fakeStore) CleaningTarget(context.Context, string, string, string, bool) (domain.Target, error) {
	return domain.Target{}, domain.ErrNotFound
}
