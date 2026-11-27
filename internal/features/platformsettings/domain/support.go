// Package domain define el contacto de soporte de la plataforma BPM.
package domain

import (
	"errors"
	"net/mail"
	"strings"
	"time"
)

// DefaultSupportEmail es el correo de soporte inicial (V22).
const DefaultSupportEmail = "proyectobpmupn@gmail.com"

var (
	ErrDenied = errors.New("platform settings access denied")
)

// ValidationError lista los campos inválidos con mensajes para la persona.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid platform settings" }

// SupportContact es el contacto de soporte visible en Ayuda.
type SupportContact struct {
	Email     string
	WhatsApp  string // solo dígitos con código de país; vacío si no hay
	UpdatedAt time.Time
}

// NormalizeSupportContact valida y limpia el correo y el WhatsApp opcional.
func NormalizeSupportContact(email, whatsapp string) (SupportContact, error) {
	fields := map[string]string{}
	email = strings.TrimSpace(email)
	if address, err := mail.ParseAddress(email); err != nil || address.Address != email || len(email) > 254 || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		fields["supportEmail"] = "Escribe un correo válido, por ejemplo soporte@empresa.com."
	}
	digits := strings.NewReplacer(" ", "", "-", "", "+", "", "(", "", ")", "").Replace(strings.TrimSpace(whatsapp))
	if digits != "" {
		valid := len(digits) >= 8 && len(digits) <= 15
		for _, c := range digits {
			if c < '0' || c > '9' {
				valid = false
			}
		}
		if !valid {
			fields["supportWhatsapp"] = "Escribe el número con código de país, por ejemplo 51987654321."
		}
	}
	if len(fields) > 0 {
		return SupportContact{}, &ValidationError{Fields: fields}
	}
	return SupportContact{Email: email, WhatsApp: digits}, nil
}
