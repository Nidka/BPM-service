// Package sender entrega eventos de webhook por HTTPS con firma HMAC-SHA256 y protección SSRF: solo conecta
// a direcciones IP públicas (resueltas en el momento de conectar), no sigue redirecciones y espera 10 s.
package sender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Nidka/BPM-service/internal/features/webhooks/domain"
)

// Sender implementa application.Sender.
type Sender struct {
	client *http.Client
	now    func() time.Time
}

// ErrBlockedAddress indica que el destino resolvió a una red interna.
var ErrBlockedAddress = errors.New("webhook: destino no público")

// New crea el emisor con un transporte que bloquea redes internas.
func New() *Sender {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if domain.PublicIP(ip.IP) {
					return dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				}
			}
			return nil, ErrBlockedAddress
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	return &Sender{now: time.Now, client: &http.Client{Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Send firma y envía el evento; un estado fuera de 2xx es un fallo (se reintenta).
func (s *Sender) Send(ctx context.Context, endpoint domain.Endpoint, delivery domain.Delivery) (int, error) {
	body, err := json.Marshal(delivery)
	if err != nil {
		return 0, err
	}
	now := s.now().UTC()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "BPM-Webhooks/1")
	req.Header.Set("X-BPM-Event", delivery.Type)
	req.Header.Set("X-BPM-Delivery", delivery.ID)
	req.Header.Set("X-BPM-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("X-BPM-Signature", domain.Signature(endpoint.Secret, now, body))
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("webhook: estado %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}
