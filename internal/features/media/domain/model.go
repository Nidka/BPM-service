// Package domain expresa las reglas de evidencias sin conocer HTTP, R2 ni SQL.
package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrDenied      = errors.New("media access denied")
	ErrNotFound    = errors.New("media not found")
	ErrConflict    = errors.New("media conflicts with existing evidence")
	ErrUnavailable = errors.New("private media storage unavailable")
	ErrNotReady    = errors.New("upload not confirmed")
	ErrInvalidFile = errors.New("uploaded content differs from reservation")
	ErrLimit       = errors.New("evidence limit reached")
)

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid evidence metadata" }

type UploadInput struct {
	ID, RunID, ItemID, IncidentID, ActionID, Stage, OriginalName, MIMEType, SHA256 string
	SizeBytes                                                                      int64
}
type File struct {
	ID, OrganizationID, LocationID, RunID, ItemID, IncidentID, ActionID, Stage, OriginalName, MIMEType, SHA256, ObjectKey, Status, CreatedBy string
	SizeBytes                                                                                                                                int64
	CreatedAt                                                                                                                                time.Time
	UploadedAt                                                                                                                               *time.Time
}
type Target struct{ OrganizationID, LocationID, RunID, ItemID, UserID, Role, RunOwner, RunStatus, IncidentID, ActionID, Stage, Reporter, Responsible, IncidentStatus, ActionStatus string }
type Policy struct {
	MaxBytes     int64
	MaxFiles     int
	AllowedMIMEs []string
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ValidID(id string) bool { return uuid.MatchString(id) }

func (p Policy) Validate(in UploadInput) (UploadInput, error) {
	fields := map[string]string{}
	if !ValidID(in.ID) {
		fields["id"] = "La foto no tiene un identificador válido."
	}
	if in.IncidentID != "" {
		if !ValidIncidentTarget(in.IncidentID, in.ActionID, in.Stage) || in.RunID != "" || in.ItemID != "" {
			fields["incidentId"] = "Elige una incidencia y una etapa de evidencia válidas."
		}
	} else if !ValidID(in.RunID) || !ValidID(in.ItemID) || in.ActionID != "" || in.Stage != "" {
		fields["itemId"] = "Elige una pregunta de esta revisión."
	}
	in.IncidentID, in.ActionID = strings.ToLower(in.IncidentID), strings.ToLower(in.ActionID)
	in.ID, in.RunID, in.ItemID = strings.ToLower(in.ID), strings.ToLower(in.RunID), strings.ToLower(in.ItemID)
	in.OriginalName = strings.TrimSpace(in.OriginalName)
	if n := utf8.RuneCountInString(in.OriginalName); n < 1 || n > 200 || strings.ContainsAny(in.OriginalName, `/\`) || strings.IndexFunc(in.OriginalName, unicode.IsControl) >= 0 {
		fields["originalName"] = "Usa un nombre de archivo sin rutas ni caracteres especiales."
	}
	allowed := false
	for _, mime := range p.AllowedMIMEs {
		if in.MIMEType == mime && (mime == "image/jpeg" || mime == "image/png") {
			allowed = true
		}
	}
	if !allowed {
		fields["mimeType"] = "Elige una foto JPG o PNG."
	}
	if in.SizeBytes < 1 || in.SizeBytes > p.MaxBytes {
		fields["sizeBytes"] = "La foto está vacía o supera el tamaño permitido."
	}
	in.SHA256 = strings.ToLower(in.SHA256)
	if !sha256Pattern.MatchString(in.SHA256) {
		fields["sha256"] = "No pudimos comprobar la integridad de la foto."
	}
	if len(fields) > 0 {
		return in, &ValidationError{Fields: fields}
	}
	return in, nil
}

func (f File) Match(in UploadInput) error {
	if f.ID != in.ID || f.RunID != in.RunID || f.ItemID != in.ItemID || f.IncidentID != in.IncidentID || f.ActionID != in.ActionID || f.Stage != in.Stage || f.OriginalName != in.OriginalName || f.MIMEType != in.MIMEType || f.SizeBytes != in.SizeBytes || f.SHA256 != in.SHA256 {
		return ErrConflict
	}
	return nil
}

// CanRead mantiene separados acceso de supervisión y ejecución de controles.
func (t Target) CanRead() bool {
	if t.IncidentID != "" {
		return t.Role == "CHEF" || t.Role == "ADMIN" || t.Role == "SUPER_ADMIN" || (t.Role == "WORKER" && (t.UserID == t.Reporter || t.UserID == t.Responsible))
	}
	return t.Role == "CHEF" || t.Role == "ADMIN" || t.Role == "SUPER_ADMIN"
}
func (t Target) CanWrite() bool {
	if t.IncidentID != "" {
		if !t.CanRead() {
			return false
		}
		if t.Stage == "BEFORE" {
			return (t.IncidentStatus == "OPEN" || t.IncidentStatus == "ASSIGNED" || t.IncidentStatus == "IN_PROGRESS") && (t.UserID == t.Reporter || t.Role == "CHEF" || t.Role == "ADMIN" || t.Role == "SUPER_ADMIN")
		}
		return (t.Role == "CHEF" || t.Role == "WORKER") && t.Stage == "AFTER" && t.IncidentStatus == "IN_PROGRESS" && t.ActionStatus == "IN_PROGRESS" && t.UserID == t.Responsible
	}
	return t.Role == "CHEF" && t.UserID == t.RunOwner && t.RunStatus == "DRAFT"
}

// ValidIncidentTarget impide mezclar la foto inicial con una acción posterior.
func ValidIncidentTarget(incident, action, stage string) bool {
	return ValidID(incident) && ((stage == "BEFORE" && action == "") || (stage == "AFTER" && ValidID(action)))
}
