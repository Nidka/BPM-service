// Package domain define los webhooks salientes (RF-48): URL HTTPS de la empresa que reciben sus alertas
// firmadas para automatizarlas con n8n u otra herramienta.
package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	MaxEndpoints = 5
	MaxURLLength = 500
	// EventTest es el evento de prueba que se envía desde la app.
	EventTest = "TEST"
)

// EventTypes son los tipos de alerta que puede recibir un webhook.
var EventTypes = []string{"CHECKLIST_MISSING", "TEMPERATURE_DANGER", "ORGANIZATION_INACTIVE", "TRAINING_ASSIGNED"}

var (
	ErrDenied   = errors.New("webhooks: denied")
	ErrNotFound = errors.New("webhooks: not found")
	ErrLimit    = errors.New("webhooks: too many endpoints")
)

// ValidationError agrupa campos inválidos (HTTP 422).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid webhook input" }

// Endpoint es un destino registrado. Secret solo viaja al crearlo o rotarlo.
type Endpoint struct {
	ID, OrganizationID, URL, Secret string
	EventTypes                      []string
	Active                          bool
	LastStatus                      *int
	LastError                       string
	LastDeliveryAt                  *time.Time
	CreatedAt, UpdatedAt            time.Time
}

// Input es lo que el administrador envía.
type Input struct {
	URL        string
	EventTypes []string
	Active     bool
}

// Normalize valida la URL (HTTPS pública, sin credenciales) y los tipos de evento.
func Normalize(in Input) (Input, error) {
	fields := map[string]string{}
	in.URL = strings.TrimSpace(in.URL)
	if msg := urlIssue(in.URL); msg != "" {
		fields["url"] = msg
	}
	types := []string{}
	for _, t := range in.EventTypes {
		t = strings.TrimSpace(t)
		if !slices.Contains(EventTypes, t) {
			fields["eventTypes"] = "Elige tipos de aviso válidos."
			break
		}
		if !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	if len(types) == 0 && fields["eventTypes"] == "" {
		fields["eventTypes"] = "Elige al menos un tipo de aviso."
	}
	in.EventTypes = types
	if len(fields) > 0 {
		return in, &ValidationError{Fields: fields}
	}
	return in, nil
}

func urlIssue(raw string) string {
	if raw == "" || len(raw) > MaxURLLength {
		return "Escribe la dirección del webhook (hasta 500 caracteres)."
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "Usa una dirección https:// válida, sin usuario ni contraseña."
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil && !PublicIP(ip) {
		return "La dirección debe ser pública (no una red interna)."
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") {
		return "La dirección debe ser pública (no una red interna)."
	}
	return ""
}

// PublicIP descarta direcciones privadas, locales, de enlace y reservadas (protección SSRF).
func PublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10 (CGNAT), 0.0.0.0/8, 192.0.0.0/24, 198.18.0.0/15, 240.0.0.0/4.
		switch {
		case v4[0] == 100 && v4[1]&0xc0 == 64, v4[0] == 0, v4[0] == 192 && v4[1] == 0 && v4[2] == 0, v4[0] == 198 && v4[1]&0xfe == 18, v4[0] >= 240:
			return false
		}
	}
	return true
}

// Signature firma timestamp + "." + cuerpo con HMAC-SHA256; n8n la verifica con el mismo secreto.
func Signature(secret string, timestamp time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Delivery es el evento que se envía: sin datos personales de quién recibe la alerta.
type Delivery struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	OrganizationID string    `json:"organizationId"`
	LocationID     string    `json:"locationId,omitempty"`
	Title          string    `json:"title"`
	Message        string    `json:"message"`
	SourceType     string    `json:"sourceType,omitempty"`
	SourceID       string    `json:"sourceId,omitempty"`
	OccurredAt     time.Time `json:"occurredAt"`
}
