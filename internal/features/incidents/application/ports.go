package application

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"time"
)

type AuthorizationStore interface {
	Actor(context.Context, string, string) (domain.Actor, error)
	Location(context.Context, domain.Actor, string, string) error
	Eligible(context.Context, string, string, string) error
	// OtherValidator indica si existe otro ADMIN activo, o CHEF activo con acceso a la sede,
	// distinto de exclude que pueda validar una corrección.
	OtherValidator(ctx context.Context, org, site, exclude string) (bool, error)
}
type CatalogStore interface {
	Catalog(context.Context, string, string) (domain.Catalog, error)
	Latest(context.Context, string, string) (domain.Catalog, error)
	InsertCatalog(context.Context, domain.Actor, string, domain.Catalog) (domain.Catalog, error)
	// EnsureDefault crea, si no existe, la categoría integrada en el catálogo de la organización y devuelve su última versión.
	EnsureDefault(context.Context, domain.Actor, string, domain.Catalog) (domain.Catalog, error)
	Catalogs(context.Context, string, pagination.Window) ([]domain.Catalog, error)
}
type IncidentStore interface {
	Incident(context.Context, string, string, bool) (domain.Incident, error)
	InsertIncident(context.Context, domain.Actor, string, domain.Report, string) (domain.Incident, error)
	SaveIncident(context.Context, domain.Actor, string, domain.Incident, int) error
	Incidents(context.Context, domain.Actor, string, string, string, pagination.Window) ([]domain.Incident, error)
	Assignees(context.Context, string, string, pagination.Window) ([]domain.Assignee, error)
	History(context.Context, string, string, pagination.Window) ([]domain.Event, error)
}
type ReceiptStore interface {
	Lock(context.Context, string) error
	Replay(context.Context, string, string, string) ([]byte, bool, error)
	ReplayOperation(context.Context, string, string, string) ([]byte, bool, error)
	Remember(context.Context, domain.Actor, string, string, string, []byte) error
	Event(context.Context, domain.Actor, string, string, string, string, string, string, string, []byte) error
	Audit(context.Context, domain.Actor, string, string, string) error
}

// NotificationStore emite el aviso al responsable de una acción recién asignada. Es de mejor esfuerzo: no devuelve
// error por esquema sin migrar ni por fallos del aviso, para no impedir la asignación.
type NotificationStore interface {
	NotifyAssigned(ctx context.Context, actor domain.Actor, org string, v domain.Incident, now time.Time) error
}
type Store interface {
	AuthorizationStore
	NotificationStore
	CatalogStore
	IncidentStore
	ReceiptStore
}
type UnitOfWork interface {
	Within(context.Context, func(Store) error) error
}
