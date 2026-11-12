// Package application contiene los casos de uso de reportes. Coordina autorización,
// validación de períodos y transacciones; las reglas viven en el dominio y el acceso a datos
// se invierte mediante los puertos de este archivo.
package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// WeeklyDelivery es una entrega semanal reclamada por el worker.
type WeeklyDelivery struct {
	ID, OrganizationID, RecipientUserID, Subject, Body string
	Attempts                                           int
}

// WeeklyOutcome es el resultado de un intento de envío.
type WeeklyOutcome struct {
	Status, ProviderID, Code string
}

// Store es el conjunto de operaciones disponibles dentro de una transacción.
type Store interface {
	// Actor devuelve el rol desde la membresía activa (o SUPER_ADMIN); ErrDenied si no pertenece.
	Actor(ctx context.Context, user, org string) (domain.Actor, error)
	Audit(ctx context.Context, org, user, action, entityType, entityID string) error
	OrganizationName(ctx context.Context, org string) (string, error)

	// LocationNames devuelve las sedes del alcance (id -> nombre); vacío si la sede pedida no está en el alcance.
	LocationNames(ctx context.Context, scope domain.Scope) (map[string]string, error)
	ComplianceRows(ctx context.Context, scope domain.Scope) ([]domain.DailyRow, error)
	TemplateRows(ctx context.Context, scope domain.Scope) ([]domain.TemplateRow, error)
	IncidentRows(ctx context.Context, scope domain.Scope) ([]domain.IncidentRow, error)
	TemperatureRows(ctx context.Context, scope domain.Scope) ([]domain.TemperatureRow, error)
	// KPIs calcula los indicadores operativos con la hora now; Training lo completa el caso de uso.
	KPIs(ctx context.Context, scope domain.Scope, now time.Time) (domain.KPIs, error)
	TrainingKPI(ctx context.Context, org string) (domain.TrainingKPI, error)
	TrainingCourses(ctx context.Context, org string) ([]domain.TrainingCourseStat, error)
	CourseExists(ctx context.Context, org, courseID string) (bool, error)
	TrainingPeople(ctx context.Context, org, courseID string, w pagination.Window) ([]domain.TrainingPerson, error)

	Settings(ctx context.Context, org string) (domain.Settings, error)
	SaveSettings(ctx context.Context, org, user string, s domain.Settings) error

	// LockCertificates serializa la emisión de constancias de una organización.
	LockCertificates(ctx context.Context, org string) error
	FindCertificate(ctx context.Context, org string, p domain.Period) (*domain.Certificate, error)
	InsertCertificate(ctx context.Context, org, user string, p domain.Period, average, threshold float64, runs int, code string) (domain.Certificate, error)
	ListCertificates(ctx context.Context, org string, w pagination.Window) ([]domain.Certificate, error)

	// CountExportRows cuenta hasta limit+1 filas exportables.
	CountExportRows(ctx context.Context, scope domain.Scope, limit int) (int, error)
	// StreamExportRows recorre las filas con un cursor; fn puede abortar devolviendo error.
	StreamExportRows(ctx context.Context, scope domain.Scope, fn func(domain.ExportRow) error) error

	// WeeklyOrganizations lista empresas activas con el correo semanal habilitado y sin entregas de la semana.
	WeeklyOrganizations(ctx context.Context, week time.Time) ([]string, error)
	// WeeklyRecipients lista los ADMIN activos con correo de la empresa.
	WeeklyRecipients(ctx context.Context, org string) ([]string, error)
	StageWeekly(ctx context.Context, org string, week time.Time, users []string, subject, body string) error
	ClaimWeekly(ctx context.Context, now time.Time, lease time.Duration) (*WeeklyDelivery, error)
	// WeeklyRecipientEmail devuelve el correo si el usuario sigue siendo ADMIN activo de una empresa activa.
	WeeklyRecipientEmail(ctx context.Context, org, user string) (string, error)
	// FinishWeekly registra el resultado; retryAt solo aplica al estado PENDING.
	FinishWeekly(ctx context.Context, d WeeklyDelivery, out WeeklyOutcome, now time.Time, retryAt time.Time) error
}

// UnitOfWork ejecuta fn en una transacción; si fn devuelve error se revierte.
type UnitOfWork interface {
	Within(ctx context.Context, fn func(Store) error) error
}

// CertificateVerifier consulta una constancia por código sin tenant (endpoint público).
type CertificateVerifier interface {
	CertificateByCode(ctx context.Context, code string) (domain.PublicCertificate, error)
}
