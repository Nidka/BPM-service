package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"github.com/jackc/pgx/v5"
	"time"
)

// storedMessage es privado del adaptador: el payload del proveedor permanece inmutable.
type storedMessage struct {
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	Subject        string `json:"subject,omitempty"`
	Text           string `json:"text,omitempty"`
	PushToken      string `json:"token,omitempty"`
	AlertID        string `json:"alertId"`
	OrganizationID string `json:"organizationId"`
	RecipientHash  string `json:"recipientHash"`
	TokenHash      string `json:"tokenHash,omitempty"`
}

func (r *Repository) Stage(ctx context.Context, org, id, from string, now time.Time) error {
	return r.stage(ctx, org, id, from, now, false)
}
func (r *Repository) stage(ctx context.Context, org, id, from string, now time.Time, pushOnly bool) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	var user, email string
	var emailEnabled, pushEnabled bool
	e = tx.QueryRow(ctx, `SELECT a.recipient_user_id,u.email,COALESCE((a.context_snapshot->>'emailEnabled')::boolean,false),COALESCE((a.context_snapshot->>'pushEnabled')::boolean,false) FROM alerts a JOIN application_users u ON u.id=a.recipient_user_id WHERE a.organization_id=$1 AND a.id=$2 AND a.attended_at IS NULL AND alert_recipient_authorized($1,a.id,a.recipient_user_id) AND alert_source_pending($1,a.id) FOR SHARE OF a`, org, id).Scan(&user, &email, &emailEnabled, &pushEnabled)
	if errors.Is(e, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if e != nil {
		return e
	}
	base := storedMessage{AlertID: id, OrganizationID: org, RecipientHash: tokenHash(user)}
	insert := func(channel, key, device string, v storedMessage) error {
		raw, e := json.Marshal(v)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO notification_deliveries(organization_id,alert_id,channel,target_key,target_device_id,request_payload,next_attempt_at,created_at) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,$7) ON CONFLICT(organization_id,alert_id,channel,target_key) DO NOTHING`, org, id, channel, key, device, raw, now)
		return e
	}
	if emailEnabled && from != "" && !pushOnly {
		v := base
		v.From = from
		v.To = email
		v.Subject = "Tienes un aviso en BPM"
		v.Text = "Tienes un aviso pendiente. Abre BPM y revisa tu bandeja de avisos."
		if e = insert("EMAIL", tokenHash(email), "", v); e != nil {
			return e
		}
	}
	if pushEnabled {
		rows, e := tx.Query(ctx, `SELECT id::text,push_token,token_hash FROM mobile_devices WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE' AND platform='ANDROID' AND push_token IS NOT NULL AND token_hash IS NOT NULL`, org, user)
		if e != nil {
			return e
		}
		type target struct{ id, token, hash string }
		targets := []target{}
		for rows.Next() {
			var t target
			if e = rows.Scan(&t.id, &t.token, &t.hash); e != nil {
				rows.Close()
				return e
			}
			targets = append(targets, t)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, t := range targets {
			v := base
			v.PushToken = t.token
			v.TokenHash = t.hash
			if e = insert("FCM", tokenHash(t.id+":"+t.hash), t.id, v); e != nil {
				return e
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if emailEnabled && from == "" && !pushOnly {
		return application.ErrUnavailable
	}
	return nil
}
func (r *Repository) Claim(ctx context.Context, channels []string, now time.Time) (*domain.Delivery, error) {
	// Un intento con resultado incierto no se repite fuera de la ventana de deduplicación.
	_, e := r.pool.Exec(ctx, `UPDATE notification_deliveries SET status=CASE WHEN attempts>0 THEN 'UNCERTAIN' ELSE 'FAILED' END,last_error='DELIVERY_EXPIRED',lease_token=NULL,locked_until=NULL WHERE (status='PENDING' OR(status='PROCESSING' AND locked_until<=$1)) AND (created_at+interval '23 hours'<=$1 OR attempts>=8)`, now)
	if e != nil {
		return nil, e
	}
	var v domain.Delivery
	var raw []byte
	e = r.pool.QueryRow(ctx, `WITH selected AS(SELECT id FROM notification_deliveries WHERE channel=ANY($1) AND attempts<8 AND created_at+interval '23 hours'>$2 AND ((status='PENDING' AND COALESCE(next_attempt_at,created_at)<=$2)OR(status='PROCESSING' AND locked_until<=$2)) ORDER BY COALESCE(next_attempt_at,created_at),id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE notification_deliveries d SET status='PROCESSING',attempts=d.attempts+1,lease_token=gen_random_uuid(),locked_until=$2+interval '60 seconds',first_attempt_at=COALESCE(d.first_attempt_at,$2),last_attempt_at=$2 FROM selected s,alerts a WHERE d.id=s.id AND a.organization_id=d.organization_id AND a.id=d.alert_id RETURNING d.id::text,d.organization_id,d.alert_id::text,a.recipient_user_id,d.channel,COALESCE(d.target_device_id::text,''),d.request_payload,d.lease_token::text,d.attempts,d.first_attempt_at`, channels, now).Scan(&v.ID, &v.OrganizationID, &v.AlertID, &v.UserID, &v.Channel, &v.DeviceID, &raw, &v.LeaseToken, &v.Attempts, &v.FirstAttemptAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var m storedMessage
	if e = json.Unmarshal(raw, &m); e != nil {
		return nil, errors.New("payload de entrega inválido")
	}
	v.Message = domain.Message{From: m.From, To: m.To, Subject: m.Subject, Text: m.Text, PushToken: m.PushToken, AlertID: m.AlertID, OrganizationID: m.OrganizationID, RecipientHash: m.RecipientHash}
	v.TokenHash = m.TokenHash
	return &v, nil
}
func (r *Repository) Eligible(ctx context.Context, v domain.Delivery) (bool, error) {
	var ok bool
	e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM alerts a JOIN application_users u ON u.id=a.recipient_user_id JOIN notification_deliveries d ON d.organization_id=a.organization_id AND d.alert_id=a.id WHERE a.organization_id=$1 AND a.id=$2 AND a.recipient_user_id=$3 AND a.attended_at IS NULL AND alert_recipient_authorized($1,a.id,$3) AND alert_source_pending($1,a.id) AND d.id=$4 AND d.lease_token=$5 AND d.locked_until>NOW() AND d.status='PROCESSING' AND (($6='EMAIL' AND u.email=$7 AND $8<>'') OR ($6='FCM' AND EXISTS(SELECT 1 FROM mobile_devices m WHERE m.organization_id=$1 AND m.id=$9::uuid AND m.user_id=$3 AND m.status='ACTIVE' AND m.platform='ANDROID' AND m.token_hash=$10 AND m.push_token=$11))))`, v.OrganizationID, v.AlertID, v.UserID, v.ID, v.LeaseToken, v.Channel, v.Message.To, v.Message.From, nullableUUID(v.DeviceID), v.TokenHash, v.Message.PushToken).Scan(&ok)
	return ok, e
}
func nullableUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (r *Repository) Finish(ctx context.Context, v domain.Delivery, out domain.Outcome, now time.Time) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	delay := time.Second * time.Duration(1<<min(v.Attempts, 10))
	delay = min(delay, 15*time.Minute)
	tag, e := tx.Exec(ctx, `UPDATE notification_deliveries SET status=$3::varchar,provider_message_id=NULLIF($4,''),last_error=NULLIF($5,''),sent_at=CASE WHEN $3::varchar='SENT' THEN $6 ELSE sent_at END,next_attempt_at=$7,lease_token=NULL,locked_until=NULL WHERE id=$1 AND lease_token=$2 AND status='PROCESSING' AND locked_until>$6`, v.ID, v.LeaseToken, out.Status, out.ProviderID, out.Code, now, now.Add(delay))
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 1 && out.InvalidToken {
		_, e = tx.Exec(ctx, `UPDATE mobile_devices SET status='INACTIVE',push_token=NULL,token_hash=NULL WHERE organization_id=$1 AND id=$2 AND user_id=$3 AND token_hash=$4`, v.OrganizationID, v.DeviceID, v.UserID, v.TokenHash)
		if e != nil {
			return e
		}
	}
	// La entrega nunca modifica el estado leído/atendido de alerts.
	return tx.Commit(ctx)
}

// Reconcile descubre destinos reemplazados sin repetir entregas ya registradas.
func (r *Repository) Reconcile(ctx context.Context, now time.Time) error {
	rows, e := r.pool.Query(ctx, `SELECT a.organization_id,a.id::text FROM alerts a WHERE a.created_at>$1::timestamptz-interval '23 hours' AND a.attended_at IS NULL AND a.status<>'CANCELLED' AND a.context_snapshot->>'pushEnabled'='true' AND alert_recipient_authorized(a.organization_id,a.id,a.recipient_user_id) AND alert_source_pending(a.organization_id,a.id) AND EXISTS(SELECT 1 FROM mobile_devices m WHERE m.organization_id=a.organization_id AND m.user_id=a.recipient_user_id AND m.platform='ANDROID' AND m.status='ACTIVE' AND m.token_hash IS NOT NULL AND NOT EXISTS(SELECT 1 FROM notification_deliveries d WHERE d.organization_id=a.organization_id AND d.alert_id=a.id AND d.channel='FCM' AND d.target_device_id=m.id AND d.request_payload->>'tokenHash'=m.token_hash)) ORDER BY a.created_at,a.id LIMIT 100`, now)
	if e != nil {
		return e
	}
	type key struct{ org, id string }
	keys := []key{}
	for rows.Next() {
		var k key
		if e = rows.Scan(&k.org, &k.id); e != nil {
			rows.Close()
			return e
		}
		keys = append(keys, k)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, k := range keys {
		if e = r.stage(ctx, k.org, k.id, "", now, true); e != nil {
			return e
		}
	}
	return nil
}
