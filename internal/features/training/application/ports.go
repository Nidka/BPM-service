// Package application contiene los casos de uso de la Academia BPM. Coordina
// autorización, idempotencia y transacciones; las reglas viven en el dominio y el
// acceso a datos y a R2 se invierte mediante los puertos de este archivo.
package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// AttemptRecord es un intento ya calificado por el servidor, listo para persistirse.
type AttemptRecord struct {
	ID, ClientOperationID, CourseID, VersionID, UserID, AttemptType, RequestHash string
	Score                                                                        int
	Passed                                                                       bool
	StartedAt, ClientCreatedAt, CompletedAt                                      time.Time
}

// AuthorizationStore resuelve el rol real del usuario en la organización.
type AuthorizationStore interface {
	// Actor devuelve el rol desde la membresía activa (o SUPER_ADMIN de plataforma);
	// ErrDenied si el usuario no pertenece a la organización.
	Actor(ctx context.Context, user, org string) (domain.Actor, error)
}

// LearnStore agrupa las lecturas y el progreso para aprender y estudiar.
type LearnStore interface {
	// CourseView lee la versión PUBLISHED de un curso PUBLISHED; userID vacío omite progreso y constancia.
	CourseView(ctx context.Context, org, userID, courseID string) (domain.CourseView, error)
	PublishedVersion(ctx context.Context, org, courseID string) (domain.Version, error)
	LessonInVersion(ctx context.Context, org, versionID, lessonID string) error
	// LessonVideo devuelve el video AVAILABLE de la lección o ErrVideoNotFound.
	LessonVideo(ctx context.Context, org, versionID, lessonID string) (domain.VideoFile, error)
	Question(ctx context.Context, org, versionID, questionID string) (domain.Question, error)
	Questions(ctx context.Context, org, versionID string) ([]domain.Question, error)
	// LockLearner serializa operaciones de un mismo usuario sobre un curso.
	LockLearner(ctx context.Context, org, user, courseID string) error
	Progress(ctx context.Context, org, user, courseID string) (domain.Progress, error)
	SaveProgress(ctx context.Context, org, user, courseID string, progress domain.Progress) error
	// FindAttempt devuelve los intentos con el mismo id o la misma clientOperationId.
	FindAttempt(ctx context.Context, org, attemptID, operationID string) ([]AttemptRecord, error)
	InsertAttempt(ctx context.Context, org string, record AttemptRecord, results []domain.Result) error
	AttemptResults(ctx context.Context, org, attemptID string) ([]domain.Result, error)
	// IssueCertificate es idempotente por (organización, usuario, versión).
	IssueCertificate(ctx context.Context, org, user, versionID, attemptID, code string) error
	// LatestCertificate devuelve la constancia vigente más reciente del usuario en el curso.
	LatestCertificate(ctx context.Context, org, user, courseID string) (*domain.Certificate, error)
}

// AuthoringStore agrupa la administración de cursos, borradores y publicación.
type AuthoringStore interface {
	ListCourses(ctx context.Context, org, status string) ([]domain.CourseSummary, error)
	CreateCourse(ctx context.Context, org, user string, course domain.NewCourse) (domain.Course, error)
	// LockCourse bloquea el curso dentro de la organización o devuelve ErrCourseNotFound.
	LockCourse(ctx context.Context, org, courseID string) (domain.Course, error)
	// Draft devuelve el borrador y found=false si no existe.
	Draft(ctx context.Context, org string, course domain.Course) (domain.Draft, bool, error)
	// CreateDraft copia la versión publicada (con IDs nuevos) o crea un borrador vacío.
	CreateDraft(ctx context.Context, org, user string, course domain.Course) (domain.Draft, error)
	ReplaceDraft(ctx context.Context, org, user string, draft domain.Draft, in domain.DraftInput) (domain.Draft, error)
	// VideoStatuses devuelve el estado de los videos de capacitación de la organización.
	VideoStatuses(ctx context.Context, org string, ids []string) (map[string]string, error)
	PublishDraft(ctx context.Context, org, user string, draft domain.Draft, now time.Time) (domain.Course, domain.Version, error)
}

// VideoStore persiste los metadatos de videos de capacitación en media_files.
type VideoStore interface {
	Video(ctx context.Context, org, id string, lock bool) (domain.VideoFile, error)
	ReserveVideo(ctx context.Context, org, user string, file domain.VideoFile) (domain.VideoFile, error)
	SetVideoStatus(ctx context.Context, org, user string, file domain.VideoFile, status string) (domain.VideoFile, error)
}

// AuditStore registra acciones sin contenido ni respuestas del cliente.
type AuditStore interface {
	Audit(ctx context.Context, org, user, action, entityType, entityID string) error
}

// Store es el conjunto de operaciones disponibles dentro de una transacción.
type Store interface {
	AuthorizationStore
	LearnStore
	AuthoringStore
	VideoStore
	AuditStore
}

// UnitOfWork ejecuta fn en una transacción; si fn devuelve error se revierte.
type UnitOfWork interface {
	Within(ctx context.Context, fn func(Store) error) error
}
