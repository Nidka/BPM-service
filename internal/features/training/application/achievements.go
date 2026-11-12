package application

import (
	"context"
	"strings"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// awardAchievements evalúa el catálogo de la organización contra los hechos del
// usuario y otorga las insignias cumplidas. Es idempotente: la clave primaria de
// user_achievements impide otorgar dos veces, aunque haya reintentos o carreras.
func (s *Service) awardAchievements(ctx context.Context, db Store, org, user, attemptID string) ([]domain.Achievement, error) {
	if err := db.EnsureAchievements(ctx, org); err != nil {
		return nil, err
	}
	catalog, err := db.Achievements(ctx, org, user)
	if err != nil {
		return nil, err
	}
	stats, err := db.AchievementStats(ctx, org, user)
	if err != nil {
		return nil, err
	}
	awarded := []domain.Achievement{}
	for _, a := range catalog {
		if a.AwardedAt != nil || !a.Met(stats) {
			continue
		}
		granted, err := db.GrantAchievement(ctx, org, user, a.ID, attemptID)
		if err != nil {
			return nil, err
		}
		if granted {
			awarded = append(awarded, a)
		}
	}
	return awarded, nil
}

// MyAchievements es el catálogo de insignias del usuario y su XP total.
type MyAchievements struct {
	Totals domain.XPTotals
	Items  []domain.Achievement
}

// MyAchievements devuelve las insignias activas (con la fecha de obtención si ya
// se lograron) y el XP total calculado por el servidor.
func (s *Service) MyAchievements(ctx context.Context, user, org string) (out MyAchievements, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrCourseNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, persister); e != nil {
			return e
		}
		if e := db.EnsureAchievements(ctx, org); e != nil {
			return e
		}
		items, e := db.Achievements(ctx, org, user)
		if e != nil {
			return e
		}
		for _, a := range items {
			if a.Active {
				out.Items = append(out.Items, a)
			}
		}
		out.Totals, e = db.XPTotals(ctx, org, user)
		return e
	})
	return out, err
}

// AchievementCatalog lista el catálogo completo para administrar (ADMIN y SUPER_ADMIN).
func (s *Service) AchievementCatalog(ctx context.Context, user, org string) (out []domain.Achievement, err error) {
	if !domain.ValidOrganization(org) {
		return nil, domain.ErrCourseNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		if e := db.EnsureAchievements(ctx, org); e != nil {
			return e
		}
		var e error
		out, e = db.Achievements(ctx, org, "")
		return e
	})
	return out, err
}

// UpdateAchievement edita nombre, puntos, umbral y vigencia de una insignia.
func (s *Service) UpdateAchievement(ctx context.Context, user, org, id string, in domain.AchievementUpdate) (out domain.Achievement, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrCourseNotFound
	}
	if id = strings.ToLower(id); !domain.ValidID(id) {
		return out, domain.ErrAchievementNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		normalized, e := in.Normalize()
		if e != nil {
			return e
		}
		if out, e = db.UpdateAchievement(ctx, org, user, id, normalized); e != nil {
			return e
		}
		return db.Audit(ctx, org, user, "training.achievement.updated", "training_achievement", id)
	})
	return out, err
}
