package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// AssignmentStore agrupa las asignaciones de cursos (RF-06) y su aviso (RF-21).
type AssignmentStore interface {
	// PublishedCourse devuelve el curso de la organización; ErrCourseNotFound si es
	// ajeno o inexistente y ErrCourseNotPublished si aún no tiene versión vigente.
	PublishedCourse(ctx context.Context, org, courseID string) (domain.Course, error)
	// ResolveTarget comprueba que persona y sede pertenezcan a la organización
	// (ErrAssigneeNotFound, ErrLocationNotFound).
	ResolveTarget(ctx context.Context, org string, target domain.AssignmentTarget) error
	FindAssignment(ctx context.Context, org, id string) (domain.Assignment, bool, error)
	// InsertAssignment devuelve ErrAssignmentExists si ya hay una activa equivalente.
	InsertAssignment(ctx context.Context, org, user string, a domain.NewAssignment) (domain.Assignment, error)
	// EnsureAssignmentAlertRule garantiza una regla TRAINING_ASSIGNED de toda la empresa
	// para que el evaluador de alertas avise a los destinatarios (no duplica el mecanismo).
	EnsureAssignmentAlertRule(ctx context.Context, org, user string) error
	ListAssignments(ctx context.Context, org string, filter domain.AssignmentFilter, w pagination.Window) ([]domain.Assignment, error)
	// CancelAssignment es idempotente: cancelar una cancelada devuelve su estado actual.
	CancelAssignment(ctx context.Context, org, user, id string, now time.Time) (domain.Assignment, error)
	MyAssignments(ctx context.Context, org, user string, w pagination.Window) ([]domain.MyAssignment, error)
	// AssignedCourseIDs lista los cursos publicados asignados al usuario.
	AssignedCourseIDs(ctx context.Context, org, user string, limit int) ([]string, error)
}

// AchievementStore agrupa insignias, XP por módulo y estadísticas (RF-10, RF-42).
type AchievementStore interface {
	// EnsureAchievements materializa el catálogo por defecto en la organización.
	EnsureAchievements(ctx context.Context, org string) error
	// Achievements lista el catálogo; con user no vacío completa AwardedAt.
	Achievements(ctx context.Context, org, user string) ([]domain.Achievement, error)
	AchievementStats(ctx context.Context, org, user string) (domain.AchievementStats, error)
	// GrantAchievement es idempotente (clave primaria): devuelve true solo si la otorgó ahora.
	GrantAchievement(ctx context.Context, org, user, achievementID, attemptID string) (bool, error)
	AttemptAchievements(ctx context.Context, org, attemptID string) ([]domain.Achievement, error)
	UpdateAchievement(ctx context.Context, org, user, id string, u domain.AchievementUpdate) (domain.Achievement, error)
	XPTotals(ctx context.Context, org, user string) (domain.XPTotals, error)
	// SaveModuleResult conserva el mejor puntaje y el mayor XP de un módulo.
	SaveModuleResult(ctx context.Context, org, user, courseID, lessonID string, score int, passed bool, xp int) error
}

// PathStore agrupa las rutas de aprendizaje (RF-40).
type PathStore interface {
	InsertPath(ctx context.Context, org, user string, in domain.PathInput) error
	// FindPath lee la ruta con sus cursos y el progreso de userID (vacío omite progreso).
	FindPath(ctx context.Context, org, userID, id string, lock bool) (domain.Path, bool, error)
	ListPaths(ctx context.Context, org, userID, status string, w pagination.Window) ([]domain.Path, error)
	// ReplacePath exige la rowVersion vigente (ErrPathConflict) y reemplaza los cursos.
	ReplacePath(ctx context.Context, org, user, id string, rowVersion int, in domain.PathInput) error
	SetPathStatus(ctx context.Context, org, user, id, status string, now time.Time) error
	// CourseStatuses devuelve el estado de los cursos de la organización (los ajenos no aparecen).
	CourseStatuses(ctx context.Context, org string, ids []string) (map[string]string, error)
}

// PublicStore resuelve la verificación pública de constancias.
type PublicStore interface {
	// CertificateByCode devuelve los datos mínimos o ErrCertificateNotFound.
	CertificateByCode(ctx context.Context, code string) (domain.PublicCertificate, error)
}

// PlusStore reúne las operaciones de la segunda entrega de la Academia.
type PlusStore interface {
	AssignmentStore
	AchievementStore
	PathStore
	PublicStore
}
