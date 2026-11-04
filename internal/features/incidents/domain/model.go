// Package domain define las incidencias, su criterio inmutable y las reglas de atención.
package domain

import (
	"errors"
	"time"
)

var (
	ErrDenied   = errors.New("incident access denied")
	ErrNotFound = errors.New("incident not found")
	ErrConflict = errors.New("incident conflict")
)

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid incident input" }

type Actor struct{ ID, Role string }
type Catalog struct {
	ID, Code, Name, Area, NormativeCriterion, RiskLevel string
	EvidenceRequired                                    bool
	VersionNumber                                       int
}
type CatalogVersion struct {
	Catalog
	ExpectedVersion int
}
type Report struct {
	ID, LocationID, CatalogID, Description, ClientOperationID string
	ClientCreatedAt                                           time.Time
}
type Command struct {
	OperationID                                          string
	RowVersion                                           int
	ActionType, ActionID, ResponsibleUserID, Description string
	DueAt                                                time.Time
	Note                                                 string
}
type Action struct {
	ID, ResponsibleUserID, ResponsibleName, Description, Status, ResolutionNote string
	DueAt                                                                       time.Time
	ResolvedAt, VerifiedAt                                                      *time.Time
	VerifiedBy                                                                  *string
}
type Incident struct {
	ID, LocationID, CatalogID, Name, Area, RiskLevel, Status, Description, ReportedBy, ReportedByName string
	CreatedAt                                                                                         time.Time
	RowVersion, CatalogVersion                                                                        int
	NormativeCriterion                                                                                string
	EvidenceRequired                                                                                  bool
	ClosedAt                                                                                          *time.Time
	Actions                                                                                           []Action
	AllowedActions                                                                                    []string
	BeforeCount, AfterCount                                                                           int
}
type Assignee struct{ ID, DisplayName, Role string }
type Event struct {
	ID, OperationID, FromStatus, ToStatus, ActorName, Note string
	CreatedAt                                              time.Time
}

func CanConfigure(role string) bool { return role == "ADMIN" || role == "SUPER_ADMIN" }
func CanSupervise(role string) bool { return CanConfigure(role) || role == "CHEF" }
func CanReport(role string) bool    { return role == "CHEF" || role == "WORKER" }
func CanRead(role string) bool      { return CanSupervise(role) || role == "WORKER" }
