// Package domain define dispositivos privados y resultados de entrega.
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
	ErrDenied   = errors.New("acceso denegado")
	ErrConflict = errors.New("vínculo no disponible")
	ErrNotFound = errors.New("dispositivo no disponible")
)

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "campos inválidos" }

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var orgPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func ValidID(v string) bool           { return idPattern.MatchString(v) }
func ValidOrganization(v string) bool { return orgPattern.MatchString(v) }
func ValidUID(v string) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) < 1 || utf8.RuneCountInString(v) > 128 {
		return false
	}
	for _, c := range v {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

type DeviceInput struct{ ID, InstallationID, Platform, PushToken string }

func (v DeviceInput) Validate() error {
	fields := map[string]string{}
	if !ValidID(v.ID) {
		fields["id"] = "Identificador inválido."
	}
	if !ValidID(v.InstallationID) {
		fields["installationId"] = "Instalación inválida."
	}
	if v.Platform != "ANDROID" && v.Platform != "IOS" && v.Platform != "WEB" {
		fields["platform"] = "Plataforma no compatible."
	}
	if !utf8.ValidString(v.PushToken) || len(v.PushToken) < 1 || len(v.PushToken) > 4096 || strings.TrimSpace(v.PushToken) != v.PushToken || strings.ContainsAny(v.PushToken, "\r\n\x00") {
		fields["pushToken"] = "Registro de notificaciones inválido."
	}
	if len(fields) > 0 {
		return &ValidationError{fields}
	}
	return nil
}

type Device struct {
	ID, Platform, Status string
	LastSeenAt           *time.Time
}

// Message conserva el destino inmutable; nunca se expone en el DTO HTTP.
type Message struct{ From, To, Subject, Text, PushToken, AlertID, OrganizationID, RecipientHash string }
type Delivery struct {
	ID, OrganizationID, AlertID, UserID, Channel, DeviceID, TokenHash, LeaseToken string
	Message                                                                       Message
	Attempts                                                                      int
	FirstAttemptAt                                                                time.Time
}
type Outcome struct {
	Status, ProviderID, Code string
	InvalidToken             bool
}
