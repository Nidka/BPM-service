package domain

import (
	"strings"
	"time"
)

// Estados persistidos de una asignación.
const (
	AssignmentActive    = "ACTIVE"
	AssignmentCancelled = "CANCELLED"
)

// Tipos de destinatario de una asignación.
const (
	TargetUser     = "USER"
	TargetRole     = "ROLE"
	TargetLocation = "LOCATION"
)

// Estado de una capacitación asignada tal como lo calcula el servidor desde el progreso.
const (
	LearnPending    = "PENDING"
	LearnInProgress = "IN_PROGRESS"
	LearnCompleted  = "COMPLETED"
	LearnOverdue    = "OVERDUE"
)

// AttemptModule es la evaluación de una sola lección (RF-42).
const AttemptModule = "MODULE"

// MaxDueHorizon limita la fecha límite de una asignación.
const MaxDueHorizon = 5 * 365 * 24 * time.Hour

// AssignmentTarget identifica a quién se asigna: una persona, un rol (con sede
// opcional) o una sede completa.
type AssignmentTarget struct {
	Type       string
	UserID     string
	Role       string
	LocationID string
}

// NewAssignment son los datos para crear una asignación. ID lo genera el cliente
// (UUID) y hace la creación idempotente.
type NewAssignment struct {
	ID, CourseID string
	Target       AssignmentTarget
	DueAt        *time.Time
}

// Normalize valida el formato y deja la solicitud canónica (UUID en minúsculas,
// fecha en UTC con microsegundos). now permite exigir fechas límite futuras.
func (a NewAssignment) Normalize(now time.Time) (NewAssignment, error) {
	fields := map[string]string{}
	a.ID = strings.ToLower(a.ID)
	a.CourseID = strings.ToLower(a.CourseID)
	a.Target.UserID = strings.TrimSpace(a.Target.UserID)
	a.Target.LocationID = strings.ToLower(strings.TrimSpace(a.Target.LocationID))
	if !ValidID(a.ID) {
		fields["id"] = "Debe ser un identificador UUID válido."
	}
	if !ValidID(a.CourseID) {
		fields["courseId"] = "Elige un curso válido."
	}
	t := a.Target
	switch t.Type {
	case TargetUser:
		if t.UserID == "" || len(t.UserID) > 128 || !validText(t.UserID, 1, 128, false) {
			fields["target.userId"] = "Indica la persona destinataria."
		}
		if t.Role != "" || t.LocationID != "" {
			fields["target"] = "Una asignación a una persona no admite rol ni sede."
		}
	case TargetRole:
		if t.Role != "ADMIN" && t.Role != "CHEF" && t.Role != "WORKER" {
			fields["target.role"] = "El rol debe ser ADMIN, CHEF o WORKER."
		}
		if t.UserID != "" {
			fields["target.userId"] = "Una asignación a un rol no admite persona."
		}
		if t.LocationID != "" && !ValidID(t.LocationID) {
			fields["target.locationId"] = "Elige una sede válida."
		}
	case TargetLocation:
		if !ValidID(t.LocationID) {
			fields["target.locationId"] = "Elige una sede válida."
		}
		if t.UserID != "" || t.Role != "" {
			fields["target"] = "Una asignación a una sede no admite persona ni rol."
		}
	default:
		fields["target.type"] = "El destinatario debe ser USER, ROLE o LOCATION."
	}
	if a.DueAt != nil {
		due := a.DueAt.UTC().Truncate(time.Microsecond)
		a.DueAt = &due
		if !due.After(now) || due.After(now.Add(MaxDueHorizon)) {
			fields["dueAt"] = "La fecha límite debe ser futura y de hasta 5 años."
		}
	}
	return a, invalid(fields)
}

// SameAs indica si dos solicitudes describen la misma asignación (para la idempotencia por id).
func (a NewAssignment) SameAs(b NewAssignment) bool {
	if a.ID != b.ID || a.CourseID != b.CourseID || a.Target != b.Target {
		return false
	}
	if (a.DueAt == nil) != (b.DueAt == nil) {
		return false
	}
	return a.DueAt == nil || a.DueAt.Equal(*b.DueAt)
}

// Assignment es una asignación persistida, con datos de lectura para administración.
type Assignment struct {
	NewAssignment
	CourseCode, CourseTitle string
	UserName, LocationName  string
	Status                  string
	CreatedAt               time.Time
	CancelledAt             *time.Time
}

// AssignmentFilter acota el listado de administración.
type AssignmentFilter struct {
	Status   string
	CourseID string
}

// MyAssignment es una capacitación asignada vista por quien debe cursarla.
type MyAssignment struct {
	AssignmentID   string
	Course         Course
	DueAt          *time.Time
	AssignedAt     time.Time
	ProgressStatus string
	CompletedAt    *time.Time
	BestScore      *int
	Status         string
	// Percent es el avance del curso (100 si está completado), calculado por lecciones.
	Percent int
}

// LearnStatus calcula el estado visible: COMPLETED si el curso está completado
// (aunque sea tarde), OVERDUE si pasó la fecha límite sin completar y, en otro
// caso, el avance real del usuario.
func LearnStatus(progress string, due *time.Time, now time.Time) string {
	switch {
	case progress == ProgressCompleted:
		return LearnCompleted
	case due != nil && now.After(*due):
		return LearnOverdue
	case progress == ProgressInProgress:
		return LearnInProgress
	default:
		return LearnPending
	}
}

// ValidAssignmentStatus valida el filtro de estado persistido.
func ValidAssignmentStatus(v string) bool { return v == AssignmentActive || v == AssignmentCancelled }
