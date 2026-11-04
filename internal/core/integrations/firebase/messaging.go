package firebaseauth

import (
	"context"
	"encoding/base64"
	"errors"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/Nidka/BPM-service/internal/core/config"
	"google.golang.org/api/option"
)

// NewMessaging conserva la credencial administrativa exclusivamente en el servidor.
func NewMessaging(ctx context.Context, cfg config.FirebaseConfig) (*messaging.Client, error) {
	raw, e := base64.StdEncoding.DecodeString(cfg.ServiceAccountJSONBase64)
	if e != nil {
		return nil, errors.New("credencial Firebase inválida")
	}
	app, e := firebase.NewApp(ctx, &firebase.Config{ProjectID: cfg.ProjectID}, option.WithCredentialsJSON(raw))
	if e != nil {
		return nil, errors.New("no se pudo inicializar Firebase Messaging")
	}
	client, e := app.Messaging(ctx)
	if e != nil {
		return nil, errors.New("no se pudo inicializar Firebase Messaging")
	}
	return client, nil
}
