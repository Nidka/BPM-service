package domain

import (
	"strings"
	"time"
)

// Criterios de insignia que evalúa solo el servidor.
const (
	CriterionCoursesCompleted = "COURSES_COMPLETED"
	CriterionPerfectScore     = "PERFECT_SCORE"
	CriterionStreakDays       = "STREAK_DAYS"
)

// XPPerCorrectModule es el XP por respuesta correcta de un módulo aprobado.
const XPPerCorrectModule = 5

// Achievement es una insignia del catálogo de una organización. AwardedAt solo se
// completa al consultar los logros de un usuario.
type Achievement struct {
	ID, Code, Name, Description string
	Points                      int
	Criterion                   string
	Threshold                   int
	Active                      bool
	AwardedAt                   *time.Time
}

// AchievementStats son los hechos del usuario con los que se evalúan los criterios.
type AchievementStats struct {
	CoursesCompleted int
	PerfectAttempts  int
	StreakDays       int
}

// Met indica si el usuario cumple el criterio. Las insignias inactivas o sin
// criterio automático nunca se otorgan.
func (a Achievement) Met(s AchievementStats) bool {
	if !a.Active {
		return false
	}
	switch a.Criterion {
	case CriterionCoursesCompleted:
		return s.CoursesCompleted >= a.Threshold
	case CriterionPerfectScore:
		return s.PerfectAttempts >= a.Threshold
	case CriterionStreakDays:
		return s.StreakDays >= a.Threshold
	}
	return false
}

// DefaultAchievements es el catálogo por defecto de la plataforma; se materializa
// en cada organización la primera vez que se necesita.
func DefaultAchievements() []Achievement {
	return []Achievement{
		{Code: "FIRST_COURSE", Name: "Primer curso completado", Description: "Completa tu primer curso de la Academia BPM.", Points: 20, Criterion: CriterionCoursesCompleted, Threshold: 1, Active: true},
		{Code: "THREE_COURSES", Name: "Tres cursos completados", Description: "Completa tres cursos de la Academia BPM.", Points: 50, Criterion: CriterionCoursesCompleted, Threshold: 3, Active: true},
		{Code: "PERFECT_SCORE", Name: "Puntaje perfecto", Description: "Obtén 100 puntos en una evaluación.", Points: 30, Criterion: CriterionPerfectScore, Threshold: 1, Active: true},
		{Code: "STREAK_3", Name: "Racha de 3 días", Description: "Realiza evaluaciones durante 3 días seguidos.", Points: 25, Criterion: CriterionStreakDays, Threshold: 3, Active: true},
	}
}

// StreakDays cuenta los días UTC consecutivos que terminan en el día más reciente
// de days (fechas distintas ordenadas de la más reciente a la más antigua).
func StreakDays(days []time.Time) int {
	if len(days) == 0 {
		return 0
	}
	streak := 1
	for i := 1; i < len(days); i++ {
		prev := days[i-1].UTC().Truncate(24 * time.Hour)
		cur := days[i].UTC().Truncate(24 * time.Hour)
		if !prev.Add(-24 * time.Hour).Equal(cur) {
			break
		}
		streak++
	}
	return streak
}

// AchievementUpdate son los campos editables por ADMIN de una insignia.
type AchievementUpdate struct {
	Name, Description string
	Points, Threshold int
	Active            bool
}

// Normalize valida los límites de la edición de una insignia.
func (u AchievementUpdate) Normalize() (AchievementUpdate, error) {
	fields := map[string]string{}
	u.Name = strings.TrimSpace(u.Name)
	u.Description = strings.TrimSpace(u.Description)
	if !validText(u.Name, 2, 120, false) {
		fields["name"] = "El nombre debe tener entre 2 y 120 caracteres."
	}
	if !validText(u.Description, 0, 300, false) {
		fields["description"] = "La descripción admite hasta 300 caracteres."
	}
	if u.Points < 0 || u.Points > 1000 {
		fields["points"] = "Los puntos deben estar entre 0 y 1000."
	}
	if u.Threshold < 1 || u.Threshold > 100 {
		fields["criterionThreshold"] = "El umbral debe estar entre 1 y 100."
	}
	return u, invalid(fields)
}

// XPTotals desglosa el XP acumulado por un usuario.
type XPTotals struct {
	Course, Module, Achievements int
}

// Total es el XP total visible para el usuario.
func (x XPTotals) Total() int { return x.Course + x.Module + x.Achievements }

// AttemptXP calcula el XP de curso y módulo que concede un intento: solo lo que suma
// por encima de lo que la persona ya tenía (mejor resultado), nunca negativo. Un FINAL
// repetido con la misma nota o un MODULE ya superado con ese puntaje conceden 0. before
// y after son el progreso del curso antes y después del intento; previousModuleXP es el
// mayor XP que la persona ya había ganado en ese módulo. Las insignias se suman aparte.
func AttemptXP(attemptType string, g Grade, before, after Progress, previousModuleXP int) int {
	switch attemptType {
	case AttemptFinal:
		return max(0, after.ExperiencePoints-before.ExperiencePoints)
	case AttemptModule:
		if g.Passed {
			return max(0, g.CorrectCount*XPPerCorrectModule-previousModuleXP)
		}
	}
	return 0
}
