package domain

import (
	"errors"
	"time"
)

var (
	ErrDenied          = errors.New("checklist access denied")
	ErrNotFound        = errors.New("checklist not found")
	ErrConflict        = errors.New("checklist conflict")
	ErrVersionConflict = errors.New("checklist version conflict")
)

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid checklist input" }

type Actor struct{ ID, PlatformRole, Role string }

type Template struct {
	ID, Name, Description, Area, VersionID string
	VersionNumber                          int
	Items                                  []Item
}

type Item struct {
	ID, SectionName, Label, ResponseType, Unit string
	DisplayOrder                               int
	Required                                   bool
	EvidenceRequiredOnFailure                  bool
	MinimumValue, MaximumValue                 *float64
}

type Run struct {
	EvidenceCounts                                                 map[string]int
	AssignmentID                                                   string
	TemplateName, LocationName, PerformedByName                    string
	CreatedAt                                                      time.Time
	ID, OrganizationID, LocationID, TemplateVersionID, PerformedBy string
	ClientOperationID                                              string
	ClientCreatedAt                                                time.Time
	Status                                                         string
	RowVersion                                                     int
	CompliancePercent                                              *float64
	SubmittedAt                                                    *time.Time
	Items                                                          []Item
	Answers                                                        []Answer
}

type Answer struct {
	ItemID, ComplianceResult, TextValue, Observation string
	NumericValue                                     *float64
}

func CanManageTemplates(role string) bool { return role == "ADMIN" || role == "SUPER_ADMIN" }
