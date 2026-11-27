package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// StaffMessage es «Avisar a mi jefe»: un aviso rápido (no es una incidencia) para el jefe de cocina y el administrador.
type StaffMessage struct {
	ID, LocationID, Kind, Text string
	CreatedAt                  time.Time
}

// Tipos de aviso rápido y su texto visible.
var staffMessageKinds = map[string]string{"SUPPLY": "Falta insumo", "EQUIPMENT": "Equipo malogrado", "HELP": "Necesito ayuda", "OTHER": "Mensaje"}

// StaffMessageLabel devuelve el texto del tipo («Falta insumo»…).
func StaffMessageLabel(kind string) string { return staffMessageKinds[kind] }

// NormalizeStaffMessage valida el aviso rápido: tipo conocido, texto opcional (obligatorio en OTHER) de hasta 500 caracteres.
func NormalizeStaffMessage(v StaffMessage) (StaffMessage, error) {
	v.ID = strings.ToLower(v.ID)
	v.LocationID = strings.ToLower(v.LocationID)
	v.Kind = strings.ToUpper(strings.TrimSpace(v.Kind))
	v.Text = strings.TrimSpace(v.Text)
	switch {
	case !ValidID(v.ID):
		return v, invalid("id")
	case !ValidID(v.LocationID):
		return v, invalid("locationId")
	case staffMessageKinds[v.Kind] == "":
		return v, invalid("kind")
	case utf8.RuneCountInString(v.Text) > 500 || strings.ContainsAny(v.Text, "\x00") || (v.Kind == "OTHER" && v.Text == ""):
		return v, invalid("text")
	}
	return v, nil
}

// SupervisorContact es a quién escribir por WhatsApp: el jefe de cocina de la sede o, si no hay, el administrador.
type SupervisorContact struct {
	Name, Role, WhatsApp string
}
