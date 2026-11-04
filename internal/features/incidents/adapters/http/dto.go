package incidentshttp

import (
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"time"
)

type catalogInput struct {
	ID                 string `json:"id"`
	Code               string `json:"code"`
	Name               string `json:"name"`
	Area               string `json:"area"`
	NormativeCriterion string `json:"normativeCriterion"`
	RiskLevel          string `json:"riskLevel"`
	EvidenceRequired   *bool  `json:"evidenceRequired"`
}

func (v catalogInput) domain() domain.Catalog {
	e := false
	if v.EvidenceRequired != nil {
		e = *v.EvidenceRequired
	}
	return domain.Catalog{ID: v.ID, Code: v.Code, Name: v.Name, Area: v.Area, NormativeCriterion: v.NormativeCriterion, RiskLevel: v.RiskLevel, EvidenceRequired: e}
}

type versionInput struct {
	ID                 string `json:"id"`
	ExpectedVersion    int    `json:"expectedVersion"`
	Name               string `json:"name"`
	Area               string `json:"area"`
	NormativeCriterion string `json:"normativeCriterion"`
	RiskLevel          string `json:"riskLevel"`
	EvidenceRequired   *bool  `json:"evidenceRequired"`
}

func (v versionInput) domain() domain.CatalogVersion {
	return domain.CatalogVersion{Catalog: catalogInput{ID: v.ID, Name: v.Name, Area: v.Area, NormativeCriterion: v.NormativeCriterion, RiskLevel: v.RiskLevel, EvidenceRequired: v.EvidenceRequired}.domain(), ExpectedVersion: v.ExpectedVersion}
}

type catalogDTO struct {
	ID                 string `json:"id"`
	Code               string `json:"code"`
	Name               string `json:"name"`
	Area               string `json:"area"`
	NormativeCriterion string `json:"normativeCriterion"`
	RiskLevel          string `json:"riskLevel"`
	EvidenceRequired   bool   `json:"evidenceRequired"`
	VersionNumber      int    `json:"versionNumber"`
}

func catalog(v domain.Catalog) catalogDTO {
	return catalogDTO{v.ID, v.Code, v.Name, v.Area, v.NormativeCriterion, v.RiskLevel, v.EvidenceRequired, v.VersionNumber}
}

type reportInput struct {
	ID                string    `json:"id"`
	LocationID        string    `json:"locationId"`
	CatalogID         string    `json:"catalogId"`
	Description       string    `json:"description"`
	ClientOperationID string    `json:"clientOperationId"`
	ClientCreatedAt   time.Time `json:"clientCreatedAt"`
}

func (v reportInput) domain() domain.Report {
	return domain.Report{ID: v.ID, LocationID: v.LocationID, CatalogID: v.CatalogID, Description: v.Description, ClientOperationID: v.ClientOperationID, ClientCreatedAt: v.ClientCreatedAt}
}

type commandInput struct {
	OperationID       string     `json:"operationId"`
	RowVersion        int        `json:"rowVersion"`
	ActionType        string     `json:"actionType"`
	ActionID          *string    `json:"actionId"`
	ResponsibleUserID *string    `json:"responsibleUserId"`
	Description       *string    `json:"description"`
	DueAt             *time.Time `json:"dueAt"`
	Note              *string    `json:"note"`
}

func (v commandInput) domain() (domain.Command, error) {
	c := domain.Command{OperationID: v.OperationID, RowVersion: v.RowVersion, ActionType: v.ActionType}
	bad := false
	if v.ActionType == "ASSIGN" {
		bad = v.Note != nil
	} else {
		bad = v.ActionID != nil || v.ResponsibleUserID != nil || v.Description != nil || v.DueAt != nil || (v.ActionType == "START" && v.Note != nil)
	}
	if bad {
		return c, &domain.ValidationError{Fields: map[string]string{"actionType": "Envía únicamente los campos de esta acción."}}
	}
	if v.ActionID != nil {
		c.ActionID = *v.ActionID
	}
	if v.ResponsibleUserID != nil {
		c.ResponsibleUserID = *v.ResponsibleUserID
	}
	if v.Description != nil {
		c.Description = *v.Description
	}
	if v.DueAt != nil {
		c.DueAt = *v.DueAt
	}
	if v.Note != nil {
		c.Note = *v.Note
	}
	return c, nil
}

type summaryDTO struct {
	ID             string    `json:"id"`
	LocationID     string    `json:"locationId"`
	CatalogID      string    `json:"catalogId"`
	Name           string    `json:"name"`
	Area           string    `json:"area"`
	RiskLevel      string    `json:"riskLevel"`
	Status         string    `json:"status"`
	Description    string    `json:"description"`
	ReportedByName string    `json:"reportedByName"`
	CreatedAt      time.Time `json:"createdAt"`
	RowVersion     int       `json:"rowVersion"`
}

func summary(v domain.Incident) summaryDTO {
	return summaryDTO{v.ID, v.LocationID, v.CatalogID, v.Name, v.Area, v.RiskLevel, v.Status, v.Description, v.ReportedByName, v.CreatedAt, v.RowVersion}
}

type actionDTO struct {
	ID                string     `json:"id"`
	ResponsibleUserID string     `json:"responsibleUserId"`
	ResponsibleName   string     `json:"responsibleName"`
	Description       string     `json:"description"`
	DueAt             time.Time  `json:"dueAt"`
	Status            string     `json:"status"`
	ResolutionNote    string     `json:"resolutionNote"`
	ResolvedAt        *time.Time `json:"resolvedAt,omitempty"`
	VerifiedAt        *time.Time `json:"verifiedAt,omitempty"`
	VerifiedBy        *string    `json:"verifiedBy,omitempty"`
}
type detailDTO struct {
	summaryDTO
	ReportedBy         string      `json:"reportedBy"`
	CatalogVersion     int         `json:"catalogVersion"`
	NormativeCriterion string      `json:"normativeCriterion"`
	EvidenceRequired   bool        `json:"evidenceRequired"`
	ClosedAt           *time.Time  `json:"closedAt,omitempty"`
	Actions            []actionDTO `json:"actions"`
	AllowedActions     []string    `json:"allowedActions"`
	BeforeCount        int         `json:"beforeCount"`
	AfterCount         int         `json:"afterCount"`
}

func detail(v domain.Incident) detailDTO {
	actions := make([]actionDTO, 0, len(v.Actions))
	for _, a := range v.Actions {
		actions = append(actions, actionDTO{a.ID, a.ResponsibleUserID, a.ResponsibleName, a.Description, a.DueAt, a.Status, a.ResolutionNote, a.ResolvedAt, a.VerifiedAt, a.VerifiedBy})
	}
	allowed := v.AllowedActions
	if allowed == nil {
		allowed = []string{}
	}
	return detailDTO{summary(v), v.ReportedBy, v.CatalogVersion, v.NormativeCriterion, v.EvidenceRequired, v.ClosedAt, actions, allowed, v.BeforeCount, v.AfterCount}
}

type assigneeDTO struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

func assignee(v domain.Assignee) assigneeDTO { return assigneeDTO{v.ID, v.DisplayName, v.Role} }

type eventDTO struct {
	ID          string    `json:"id"`
	OperationID string    `json:"operationId"`
	FromStatus  string    `json:"fromStatus"`
	ToStatus    string    `json:"toStatus"`
	ActorName   string    `json:"actorName"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"createdAt"`
}

func event(v domain.Event) eventDTO {
	return eventDTO{v.ID, v.OperationID, v.FromStatus, v.ToStatus, v.ActorName, v.Note, v.CreatedAt}
}
