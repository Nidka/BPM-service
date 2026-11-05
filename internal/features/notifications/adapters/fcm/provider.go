// Package fcm traduce avisos genéricos al SDK administrativo de Firebase.
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
	id, e := p.client.Send(ctx, &messaging.Message{Token: v.PushToken, Data: map[string]string{"alertId": v.AlertID, "organizationId": v.OrganizationID, "recipientHash": v.RecipientHash}, Android: &messaging.AndroidConfig{CollapseKey: v.AlertID, Priority: "normal", TTL: &ttl}})
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
