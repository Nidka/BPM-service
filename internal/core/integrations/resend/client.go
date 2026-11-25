// Package resend es el cliente HTTP compartido de la API de Resend (https://api.resend.com/emails).
//
// Es infraestructura técnica: no conoce plantillas ni reglas de negocio. Nunca registra direcciones, asuntos, cuerpos ni la
// clave de API; los errores que devuelve son sentinelas sin datos del mensaje.
package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Endpoint es el único destino al que se envía la clave de API.
const Endpoint = "https://api.resend.com/emails"

var (
	// ErrNotConfigured indica que no hay clave de API; el llamador debe usar su alternativa.
	ErrNotConfigured = errors.New("resend: proveedor de correo no configurado")
	// ErrTemporary indica un fallo transitorio (red, 429 o 5xx): reintentar o usar la alternativa es seguro.
	ErrTemporary = errors.New("resend: proveedor de correo temporalmente no disponible")
	// ErrRejected indica un rechazo definitivo (4xx): reintentar el mismo mensaje no ayuda.
	ErrRejected = errors.New("resend: el proveedor rechazó el correo")
)

// Email es un mensaje listo para enviar.
type Email struct {
	From    string
	To      string
	ReplyTo string
	Subject string
	HTML    string
	Text    string
	// IdempotencyKey evita duplicados si el mismo envío se reintenta; opcional.
	IdempotencyKey string
}

// Client envía correo mediante Resend. El valor cero no es válido: usar New.
type Client struct {
	http     *http.Client
	key      string
	endpoint string
}

// New crea el cliente. Con una clave vacía Send devuelve ErrNotConfigured sin abrir conexiones.
func New(client *http.Client, apiKey string) *Client {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	copied := *client
	// Una redirección podría reenviar la clave a otro host.
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &copied, key: strings.TrimSpace(apiKey), endpoint: Endpoint}
}

// Configured indica si hay clave de API.
func (c *Client) Configured() bool { return c != nil && c.key != "" }

// WithEndpoint cambia el destino; solo para pruebas con un servidor local.
func (c *Client) WithEndpoint(endpoint string) *Client {
	copied := *c
	copied.endpoint = endpoint
	return &copied
}

type payload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	ReplyTo string   `json:"reply_to,omitempty"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html,omitempty"`
	Text    string   `json:"text,omitempty"`
}

// Send entrega el correo y devuelve el identificador asignado por Resend.
func (c *Client) Send(ctx context.Context, email Email) (string, error) {
	if !c.Configured() {
		return "", ErrNotConfigured
	}
	if email.From == "" || email.To == "" || email.Subject == "" || (email.HTML == "" && email.Text == "") ||
		strings.ContainsAny(email.From+email.To+email.ReplyTo+email.Subject, "\r\n") {
		return "", ErrRejected
	}
	body, err := json.Marshal(payload{From: email.From, To: []string{email.To}, ReplyTo: email.ReplyTo, Subject: email.Subject, HTML: email.HTML, Text: email.Text})
	if err != nil {
		return "", ErrRejected
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", ErrRejected
	}
	request.Header.Set("Authorization", "Bearer "+c.key)
	request.Header.Set("Content-Type", "application/json")
	if email.IdempotencyKey != "" {
		request.Header.Set("Idempotency-Key", email.IdempotencyKey)
	}
	result, err := c.http.Do(request)
	if err != nil {
		return "", ErrTemporary
	}
	defer result.Body.Close()
	if result.StatusCode == http.StatusTooManyRequests || result.StatusCode >= 500 {
		return "", ErrTemporary
	}
	// 401/403 (clave inválida o dominio sin verificar) y 422 son errores de configuración o de datos, no del mensaje en sí: el
	// llamador decide si usa su alternativa; para el cliente de la app equivale a «no disponible».
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return "", ErrRejected
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return "", ErrTemporary
	}
	var output struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &output) != nil || strings.TrimSpace(output.ID) == "" || len(output.ID) > 200 {
		return "", ErrTemporary
	}
	return output.ID, nil
}
