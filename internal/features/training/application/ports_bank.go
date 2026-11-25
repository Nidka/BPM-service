package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// BankStore agrupa lo que necesita el banco oficial de preguntas: sorteo persistido de cada evaluación,
// práctica y repaso espaciado, y búsqueda de cursos por código para instalar el catálogo.
type BankStore interface {
	// FindDraw devuelve el sorteo con ese id (cualquier persona); found=false si no existe.
	FindDraw(ctx context.Context, org, id string) (domain.Draw, bool, error)
	InsertDraw(ctx context.Context, org string, draw domain.Draw) error
	// ItemHistory cuenta cuántas veces vio la persona cada ítem en evaluaciones ya terminadas del curso.
	ItemHistory(ctx context.Context, org, user, courseID string) ([]domain.ItemUse, error)
	// RecordPractice guarda una respuesta de práctica y devuelve el XP ganado (solo el primer acierto de cada ítem).
	RecordPractice(ctx context.Context, org, user, courseID, itemCode string, correct bool, now time.Time) (xp int, err error)
	// ReviewDue lista las preguntas de la versión que toca repasar (falladas o vencidas), las más urgentes primero.
	ReviewDue(ctx context.Context, org, user, courseID, versionID string, now time.Time) (domain.ReviewInfo, error)
	// CourseByCode busca un curso de la organización por su código.
	CourseByCode(ctx context.Context, org, code string) (domain.Course, bool, error)
	// PublishedBankVersion devuelve la versión del banco de la versión publicada del curso (0 si no usa banco).
	PublishedBankVersion(ctx context.Context, org, courseID string) (int, error)
}
