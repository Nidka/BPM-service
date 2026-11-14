package domain

import "errors"

var (
	ErrDenied            = errors.New("workspace access denied")
	ErrConflict          = errors.New("resource already exists")
	ErrMemberUnavailable = errors.New("member unavailable")
)

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid workspace input" }

type Actor struct{ ID, Role string }
type Organization struct {
	ID, LegalName, TradeName, TaxID, BusinessType, ContactName, ContactPhone, ContactEmail string
	// RowVersion es 0 en altas y entradas; las lecturas devuelven la versión vigente.
	RowVersion int
}
type Location struct {
	ID, Name, Address, District, ContactPhone, Timezone string
	// Status es ACTIVE/INACTIVE; vacío en altas. RowVersion es 0 en altas.
	Status     string
	RowVersion int
}
type Member struct {
	ID, Name, Email, Role string
	LocationIDs           []string
	// Status es el estado de la membresía; RowVersion su versión optimista.
	Status     string
	RowVersion int
	// AccountActive indica si la cuenta BPM sigue activa (no se expone por API).
	AccountActive bool
}
type Entry struct {
	ID, Title, Detail, Status string
	// CursorKey conserva la clave de orden; nunca forma parte del DTO público.
	CursorKey string
}

type Workspace struct {
	Organization                   Organization
	Role                           string
	NextTokens                     map[string]string
	Totals                         map[string]int
	Locations                      []Location
	Members                        []Member
	Courses, Checklists, Incidents []Entry
}

func CanManage(role string) bool { return role == "SUPER_ADMIN" || role == "ADMIN" }
