package domain

import (
	"errors"
	"time"
)

// Errores de la gestión de cuentas. Cada uno tiene un código HTTP estable en el adaptador.
var (
	ErrNotFound           = errors.New("resource not found")
	ErrVersionConflict    = errors.New("stale row version")
	ErrLastAdmin          = errors.New("organization must keep an active admin")
	ErrMemberExists       = errors.New("member already exists")
	ErrInvitationPending  = errors.New("invitation already pending")
	ErrInvitationNotFound = errors.New("invitation not found")
	ErrInvitationExpired  = errors.New("invitation expired")
	ErrInvitationUsed     = errors.New("invitation already used")
	ErrInvitationRevoked  = errors.New("invitation revoked")
	ErrInvitationEmail    = errors.New("invitation email mismatch")
	ErrInvitationNotOpen  = errors.New("invitation is not pending")
	ErrResendTooSoon      = errors.New("invitation resent too soon")
	ErrMemberInactive     = errors.New("member inactive")
)

// Roles empresariales válidos.
const (
	RoleAdmin  = "ADMIN"
	RoleChef   = "CHEF"
	RoleWorker = "WORKER"
)

// Estados de invitación. EXPIRED es el estado efectivo de una pendiente vencida.
const (
	InvitationPending  = "PENDING"
	InvitationAccepted = "ACCEPTED"
	InvitationRevoked  = "REVOKED"
	InvitationExpired  = "EXPIRED"
)

// InvitationTTL es la vigencia de una invitación, renovada al reenviarla.
const InvitationTTL = 7 * 24 * time.Hour

// ResendCooldown es la espera mínima entre dos envíos de la misma invitación.
const ResendCooldown = time.Minute

// Invitation nunca contiene el token ni su hash.
type Invitation struct {
	ID, Email, Role, Status string
	LocationIDs             []string
	OrganizationID          string
	ExpiresAt, LastSentAt   time.Time
	CreatedAt               time.Time
	SendCount, RowVersion   int
	// TokenHash solo viaja entre dominio y almacenamiento.
	TokenHash string
	// AcceptedBy se rellena al aceptar.
	AcceptedBy string
}

// Accepted es el resultado de aceptar una invitación.
type Accepted struct {
	OrganizationID, Role string
	LocationIDs          []string
}

// Device describe un dispositivo móvil propio; nunca incluye el token push.
type Device struct {
	ID, OrganizationID, OrganizationName, Platform, Status string
	LastSeenAt                                             *time.Time
	CreatedAt                                              time.Time
}

// MemberChange agrupa los cambios que se guardan sobre una membresía.
type MemberChange struct {
	Role, Status string
	// Locations nil conserva las sedes; una lista vacía las retira.
	Locations *[]string
	Action    string
	Fields    []string
}

// BusinessTypes son los tipos de establecimiento válidos (RF-49).
func ValidBusinessType(v string) bool {
	switch v {
	case "RESTAURANT", "CAFETERIA", "CATERING", "OTHER_FOOD_SERVICE":
		return true
	}
	return false
}
