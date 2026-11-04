package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var code = regexp.MustCompile(`^[A-Z0-9_-]{1,40}$`)
var organization = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func ValidID(v string) bool           { return uuid.MatchString(v) }
func ValidOrganization(v string) bool { return organization.MatchString(v) }
func ValidStatus(v string) bool {
	return v == "OPEN" || v == "ASSIGNED" || v == "IN_PROGRESS" || v == "RESOLVED" || v == "CLOSED" || v == "VERIFIED"
}
func text(v string, min, max int) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) >= min && utf8.RuneCountInString(v) <= max && strings.IndexFunc(v, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) < 0
}
func invalid(field string) error {
	return &ValidationError{Fields: map[string]string{field: "Revisa el valor ingresado."}}
}
func NormalizeCatalog(v Catalog, create bool) (Catalog, error) {
	v.ID = strings.ToLower(v.ID)
	v.Name = strings.TrimSpace(v.Name)
	v.Area = strings.TrimSpace(v.Area)
	v.NormativeCriterion = strings.TrimSpace(v.NormativeCriterion)
	for _, x := range []struct {
		k  string
		ok bool
	}{{"id", ValidID(v.ID)}, {"code", !create || code.MatchString(v.Code)}, {"name", text(v.Name, 2, 200)}, {"area", text(v.Area, 2, 100)}, {"normativeCriterion", text(v.NormativeCriterion, 1, 4000)}, {"riskLevel", v.RiskLevel == "LOW" || v.RiskLevel == "MEDIUM" || v.RiskLevel == "HIGH" || v.RiskLevel == "CRITICAL"}} {
		if !x.ok {
			return v, invalid(x.k)
		}
	}
	return v, nil
}
func NormalizeReport(v Report, now time.Time) (Report, error) {
	v.ID = strings.ToLower(v.ID)
	v.LocationID = strings.ToLower(v.LocationID)
	v.CatalogID = strings.ToLower(v.CatalogID)
	v.ClientOperationID = strings.ToLower(v.ClientOperationID)
	v.Description = strings.TrimSpace(v.Description)
	v.ClientCreatedAt = v.ClientCreatedAt.UTC().Truncate(time.Microsecond)
	for _, x := range []struct {
		k  string
		ok bool
	}{{"id", ValidID(v.ID)}, {"locationId", ValidID(v.LocationID)}, {"catalogId", ValidID(v.CatalogID)}, {"clientOperationId", ValidID(v.ClientOperationID)}, {"description", text(v.Description, 1, 4000)}, {"clientCreatedAt", !v.ClientCreatedAt.IsZero() && !v.ClientCreatedAt.After(now.Add(5*time.Minute))}} {
		if !x.ok {
			return v, invalid(x.k)
		}
	}
	return v, nil
}
func NormalizeCommand(v Command, now time.Time) (Command, error) {
	v.OperationID = strings.ToLower(v.OperationID)
	v.ActionID = strings.ToLower(v.ActionID)
	v.Description = strings.TrimSpace(v.Description)
	v.Note = strings.TrimSpace(v.Note)
	v.DueAt = v.DueAt.UTC().Truncate(time.Microsecond)
	if !ValidID(v.OperationID) {
		return v, invalid("operationId")
	}
	if v.RowVersion < 1 {
		return v, invalid("rowVersion")
	}
	if v.ActionType == "ASSIGN" {
		if !ValidID(v.ActionID) {
			return v, invalid("actionId")
		}
		if !text(v.ResponsibleUserID, 1, 128) {
			return v, invalid("responsibleUserId")
		}
		if !text(v.Description, 1, 2000) {
			return v, invalid("description")
		}
		if v.DueAt.IsZero() {
			return v, invalid("dueAt")
		}
		if v.Note != "" {
			return v, invalid("note")
		}
	} else {
		if v.ActionID != "" || v.ResponsibleUserID != "" || v.Description != "" || !v.DueAt.IsZero() {
			return v, invalid("actionType")
		}
		switch v.ActionType {
		case "START":
			if v.Note != "" {
				return v, invalid("note")
			}
		case "RESOLVE", "VERIFY", "RETURN":
			if !text(v.Note, 1, 2000) {
				return v, invalid("note")
			}
		default:
			return v, invalid("actionType")
		}
	}
	return v, nil
}
