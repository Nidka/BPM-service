// Package resend entrega correo mediante el endpoint HTTPS fijo de Resend.
package resend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"io"
	"net/http"
	"strings"
)

type Provider struct {
	client *http.Client
	key    string
}

func New(client *http.Client, key string) *Provider {
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Provider{&copyClient, key}
}
func (p *Provider) Send(ctx context.Context, key string, v domain.Message) (string, error) {
	body, e := json.Marshal(struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
	}{v.From, []string{v.To}, v.Subject, v.Text})
	if e != nil {
		return "", errors.New("correo inválido")
	}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.resend.com/emails", bytes.NewReader(body))
	if e != nil {
		return "", errors.New("solicitud de correo inválida")
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	result, e := p.client.Do(req)
	if e != nil {
		return "", errors.New("entrega de correo incierta")
	}
	defer result.Body.Close()
	if result.StatusCode == 429 || result.StatusCode >= 500 {
		return "", errors.New("proveedor de correo temporalmente no disponible")
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return "", application.ErrPermanent
	}
	raw, e := io.ReadAll(io.LimitReader(result.Body, 65537))
	if e != nil || len(raw) > 65536 {
		return "", errors.New("confirmación de correo incierta")
	}
	var output struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &output) != nil || strings.TrimSpace(output.ID) == "" || len(output.ID) > 200 {
		return "", errors.New("confirmación de correo incierta")
	}
	return output.ID, nil
}
