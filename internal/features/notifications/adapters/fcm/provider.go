// Package fcm traduce avisos al SDK administrativo de Firebase: texto específico por tipo y datos de navegación.
package fcm

import (
	"context"
	"errors"
	"firebase.google.com/go/v4/errorutils"
	"firebase.google.com/go/v4/messaging"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"time"
)

type Client interface {
	Send(context.Context, *messaging.Message) (string, error)
}
type Provider struct{ client Client }

func New(c Client) *Provider { return &Provider{c} }
func (p *Provider) Send(ctx context.Context, _ string, v domain.Message) (string, error) {
	ttl := time.Hour
	title, body := v.Push.Title, v.Push.Body
	if title == "" || body == "" {
		// Tipo desconocido o entrega preparada antes de existir el texto específico.
		title, body = domain.GenericPushTitle, domain.GenericPushBody
	}
	priority := "normal"
	if v.Push.Type == domain.AlertIncidentReported {
		// Solo los reportes urgentes generan push de este tipo: se entregan con prioridad alta.
		priority = "high"
	}
	id, e := p.client.Send(ctx, &messaging.Message{
		Token:        v.PushToken,
		Notification: &messaging.Notification{Title: domain.Truncate(title, domain.PushTitleMax), Body: domain.Truncate(body, domain.PushBodyMax)},
		Data:         data(v),
		// La etiqueta reemplaza una notificación repetida del mismo aviso (FCM entrega al menos una vez).
		Android: &messaging.AndroidConfig{CollapseKey: v.AlertID, Priority: priority, TTL: &ttl, Notification: &messaging.AndroidNotification{ChannelID: "bpm-alerts-v2", Tag: v.AlertID}},
	})
	if messaging.IsRegistrationTokenNotRegistered(e) {
		return "", application.ErrInvalidToken
	}
	if messaging.IsInvalidArgument(e) || messaging.IsMismatchedCredential(e) || errorutils.IsUnauthenticated(e) || errorutils.IsPermissionDenied(e) {
		return "", application.ErrPermanent
	}
	if e != nil {
		return "", errors.New("entrega push incierta")
	}
	if id == "" {
		return "", errors.New("confirmación push incierta")
	}
	return id, nil
}

// data conserva las tres claves históricas y agrega navegación estable. Solo viajan identificadores y
// una ruta; nunca tokens, correos ni nombres de personas. Las claves vacías se omiten.
func data(v domain.Message) map[string]string {
	out := map[string]string{"alertId": v.AlertID, "organizationId": v.OrganizationID, "recipientHash": v.RecipientHash}
	if v.Push.Route == "" {
		out["route"] = "avisos"
	}
	for k, val := range map[string]string{"type": v.Push.Type, "sourceType": v.Push.SourceType, "entityId": v.Push.EntityID, "siteId": v.Push.SiteID, "route": v.Push.Route} {
		if val != "" {
			out[k] = val
		}
	}
	return out
}
