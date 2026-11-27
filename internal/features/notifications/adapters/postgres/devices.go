package postgres

import (
	"context"
	"encoding/json"
	"time"

	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/outbox"
	"github.com/Nidka/BPM-service/internal/features/notifications/application"
	"github.com/Nidka/BPM-service/internal/features/notifications/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
)

// Repository persiste dispositivos y entregas. logger es opcional: solo registra conteos y códigos, nunca tokens ni textos.
type Repository struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}
type deviceStore struct{ tx pgx.Tx }

func New(p *pgxpool.Pool) *Repository { return &Repository{pool: p} }

// WithLogger activa el registro de la preparación de entregas (qué canales y cuántos dispositivos se prepararon).
func (r *Repository) WithLogger(l *slog.Logger) *Repository {
	r.logger = l
	return r
}
func (r *Repository) Within(ctx context.Context, fn func(application.DeviceStore) error) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = fn(&deviceStore{tx}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *deviceStore) Authorize(ctx context.Context, user, org string) error {
	var ok bool
	e := s.tx.QueryRow(ctx, `SELECT true FROM application_users u JOIN organization_memberships m ON m.user_id=u.id AND m.organization_id=$2 AND m.status='ACTIVE' JOIN organizations o ON o.id=m.organization_id AND o.status='ACTIVE' WHERE u.id=$1 AND u.status='ACTIVE' FOR SHARE OF u,m,o`, user, org).Scan(&ok)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrDenied
	}
	return e
}
func tokenHash(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
func conflict(e error) error {
	var pg *pgconn.PgError
	if errors.As(e, &pg) && pg.Code == "23505" {
		return domain.ErrConflict
	}
	return e
}
func (s *deviceStore) Register(ctx context.Context, user, org string, v domain.DeviceInput) (out domain.Device, e error) {
	hash := tokenHash(v.PushToken)
	// El mismo token no puede ligarse a otra identidad, aun en otra organización.
	_, e = s.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('push-token:'||$1,0))`, hash)
	if e != nil {
		return
	}
	var taken bool
	e = s.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mobile_devices WHERE user_id<>$1 AND ((status='ACTIVE' AND (token_hash=$2 OR push_token=$3)) OR id=$4::uuid OR (organization_id=$5 AND installation_id=$6)))`, user, hash, v.PushToken, v.ID, org, v.InstallationID).Scan(&taken)
	if e != nil {
		return
	}
	if taken {
		return out, domain.ErrConflict
	}
	// Rotación propia: el ID y la instalación deben seguir correspondiendo al dueño.
	e = s.tx.QueryRow(ctx, `INSERT INTO mobile_devices(id,organization_id,user_id,installation_id,platform,push_token,token_hash,last_seen_at,status,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,NOW(),'ACTIVE',$3,$3) ON CONFLICT(id) DO UPDATE SET push_token=EXCLUDED.push_token,token_hash=EXCLUDED.token_hash,platform=EXCLUDED.platform,status='ACTIVE',last_seen_at=NOW(),updated_by=$3 WHERE mobile_devices.organization_id=$2 AND mobile_devices.user_id=$3 AND mobile_devices.installation_id=$4 RETURNING id::text,platform,status,last_seen_at`, v.ID, org, user, v.InstallationID, v.Platform, v.PushToken, hash).Scan(&out.ID, &out.Platform, &out.Status, &out.LastSeenAt)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrConflict
	}
	return out, conflict(e)
}
func (s *deviceStore) Deactivate(ctx context.Context, user, org, id string) (out domain.Device, e error) {
	e = s.tx.QueryRow(ctx, `UPDATE mobile_devices SET status='INACTIVE',push_token=NULL,token_hash=NULL,updated_by=$3 WHERE organization_id=$1 AND id=$2 AND user_id=$3 RETURNING id::text,platform,status,last_seen_at`, org, id, user).Scan(&out.ID, &out.Platform, &out.Status, &out.LastSeenAt)
	if errors.Is(e, pgx.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return
}

// TestPush crea el aviso TEST_PUSH (solo para la persona, sin sede) y su evento de envío. Requiere V24, un celular activo
// con token y que no haya otra prueba en el último minuto. El aviso queda marcado como prueba en la bandeja y su envío
// vence a los 10 minutos.
func (s *deviceStore) TestPush(ctx context.Context, user, org string) error {
	var ok bool
	if e := s.tx.QueryRow(ctx, `SELECT to_regprocedure('alert_v24_supported()') IS NOT NULL`).Scan(&ok); e != nil || !ok {
		return domain.ErrUnavailable
	}
	if _, e := s.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('test-push:'||$1||':'||$2))`, org, user); e != nil {
		return e
	}
	var devices, recent int
	if e := s.tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM mobile_devices WHERE organization_id=$1 AND user_id=$2 AND status='ACTIVE' AND push_token IS NOT NULL),
 (SELECT count(*) FROM alerts WHERE organization_id=$1 AND recipient_user_id=$2 AND alert_type='TEST_PUSH' AND created_at > NOW() - INTERVAL '1 minute')`, org, user).Scan(&devices, &recent); e != nil {
		return e
	}
	if devices == 0 {
		return domain.ErrNoDevice
	}
	if recent > 0 {
		return domain.ErrRateLimited
	}
	var id string
	if e := s.tx.QueryRow(ctx, `INSERT INTO alerts(organization_id,location_id,recipient_user_id,alert_type,title,message,source_type,source_id,dedup_key,context_snapshot,created_by,updated_by)
 SELECT $1,NULL,$2,'TEST_PUSH','Notificación de prueba','Prueba enviada desde Ajustes. Puedes ignorarla.','TEST',x.id,'test-push:'||x.id,'{"emailEnabled":false,"pushEnabled":true,"test":true,"configuration":{}}'::jsonb,$2,$2 FROM (SELECT gen_random_uuid()::text AS id) x
 RETURNING id::text`, org, user).Scan(&id); e != nil {
		return e
	}
	return outbox.Enqueue(ctx, s.tx, outbox.Event{OrganizationID: org, Type: "notifications.alert.ready.v1", AggregateType: "alert", AggregateID: id, DedupKey: "alert:" + id, Payload: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute)})
}
