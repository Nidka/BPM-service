package traininghttp

import (
	"context"
	"sort"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// bankState es el estado en memoria del banco: sorteos y práctica.
type bankState struct {
	draws    map[string]domain.Draw
	practice map[string]domain.PracticeState // "usuario|curso|ítem"
}

func newBankState() bankState {
	return bankState{draws: map[string]domain.Draw{}, practice: map[string]domain.PracticeState{}}
}

func (f *fakeRepo) FindDraw(_ context.Context, _, id string) (domain.Draw, bool, error) {
	d, ok := f.bank.draws[id]
	return d, ok, nil
}

func (f *fakeRepo) InsertDraw(_ context.Context, _ string, d domain.Draw) error {
	f.bank.draws[d.ID] = d
	return nil
}

func (f *fakeRepo) ItemHistory(_ context.Context, _, user, courseID string) ([]domain.ItemUse, error) {
	uses := map[string]*domain.ItemUse{}
	for _, a := range f.attempts {
		if a.UserID != user || a.CourseID != courseID {
			continue
		}
		for _, r := range f.results[a.ID] {
			if r.ItemCode == "" {
				continue
			}
			u := uses[r.ItemCode]
			if u == nil {
				u = &domain.ItemUse{ItemCode: r.ItemCode}
				uses[r.ItemCode] = u
			}
			u.Uses++
			if a.CompletedAt.After(u.LastUsed) {
				u.LastUsed = a.CompletedAt
			}
		}
	}
	out := []domain.ItemUse{}
	for _, u := range uses {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ItemCode < out[j].ItemCode })
	return out, nil
}

func (f *fakeRepo) RecordPractice(_ context.Context, _, user, courseID, itemCode string, correct bool, now time.Time) (int, error) {
	key := user + "|" + courseID + "|" + itemCode
	next, gained := domain.NextPractice(f.bank.practice[key], correct, now)
	f.bank.practice[key] = next
	return gained, nil
}

func (f *fakeRepo) ReviewDue(_ context.Context, org, user, courseID, versionID string, now time.Time) (domain.ReviewInfo, error) {
	info := domain.ReviewInfo{QuestionIDs: []string{}}
	v := f.versionByID(org, versionID)
	if v == nil {
		return info, nil
	}
	for _, q := range v.questions {
		if p, ok := f.bank.practice[user+"|"+courseID+"|"+q.ItemCode]; ok && q.ItemCode != "" && p.DueAt != nil && !p.DueAt.After(now) {
			info.DueCount++
			if len(info.QuestionIDs) < domain.ReviewBatch {
				info.QuestionIDs = append(info.QuestionIDs, q.ID)
			}
		}
	}
	return info, nil
}

func (f *fakeRepo) CourseByCode(_ context.Context, org, code string) (domain.Course, bool, error) {
	for _, c := range f.courses {
		if c.org == org && c.c.Code == code {
			return c.c, true, nil
		}
	}
	return domain.Course{}, false, nil
}

func (f *fakeRepo) PublishedBankVersion(_ context.Context, org, courseID string) (int, error) {
	c, err := f.course(org, courseID)
	if err != nil {
		return 0, err
	}
	version := 0
	if v := c.withStatus(domain.StatusPublished); v != nil {
		for _, q := range v.questions {
			if q.BankVersion > version {
				version = q.BankVersion
			}
		}
	}
	return version, nil
}
