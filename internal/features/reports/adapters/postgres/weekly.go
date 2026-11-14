package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/application"
	"github.com/jackc/pgx/v5"
)

// WeeklyOrganizations lista empresas activas con el correo habilitado y sin entregas de la semana.
func (s *store) WeeklyOrganizations(ctx context.Context, week time.Time) ([]string, error) {
	rows, err := s.tx.Query(ctx, `SELECT o.id FROM organizations o LEFT JOIN report_settings rs ON rs.organization_id=o.id
 WHERE o.status='ACTIVE' AND COALESCE(rs.weekly_email_enabled,TRUE)
 AND NOT EXISTS(SELECT 1 FROM weekly_report_deliveries d WHERE d.organization_id=o.id AND d.week_start=$1::date)
 ORDER BY o.id LIMIT 500`, week)
	return collectStrings(rows, err)
}

// WeeklyRecipients lista los ADMIN activos con un correo utilizable.
func (s *store) WeeklyRecipients(ctx context.Context, org string) ([]string, error) {
	rows, err := s.tx.Query(ctx, `SELECT m.user_id FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE'
 WHERE m.organization_id=$1 AND m.status='ACTIVE' AND m.role='ADMIN' AND position('@' in u.email)>1 AND u.email !~ '[[:cntrl:]]' ORDER BY m.user_id`, org)
	return collectStrings(rows, err)
}

func collectStrings(rows pgx.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// StageWeekly programa una entrega por administrador; la unicidad impide duplicados.
func (s *store) StageWeekly(ctx context.Context, org string, week time.Time, users []string, subject, body string) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO weekly_report_deliveries(organization_id,week_start,recipient_user_id,subject,body_text)
 SELECT $1,$2::date,u,$4,$5 FROM unnest($3::text[]) AS u ON CONFLICT (organization_id,week_start,recipient_user_id) DO NOTHING`, org, week, users, subject, body)
	return err
}

// ClaimWeekly reclama una entrega vencida con un lease; recupera las abandonadas tras su expiración.
func (s *store) ClaimWeekly(ctx context.Context, now time.Time, lease time.Duration) (*application.WeeklyDelivery, error) {
	var d application.WeeklyDelivery
	err := s.tx.QueryRow(ctx, `UPDATE weekly_report_deliveries d SET status='PROCESSING', attempts=d.attempts+1, lease_token=gen_random_uuid(), locked_until=$1::timestamptz+($2::bigint*interval '1 second'), updated_at=$1
 WHERE d.id=(SELECT x.id FROM weekly_report_deliveries x WHERE (x.status='PENDING' AND x.next_attempt_at<=$1) OR (x.status='PROCESSING' AND x.locked_until<=$1)
  ORDER BY x.next_attempt_at, x.id FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING d.id::text, d.organization_id, d.recipient_user_id, d.subject, d.body_text, d.attempts`, now, int64(lease/time.Second)).
		Scan(&d.ID, &d.OrganizationID, &d.RecipientUserID, &d.Subject, &d.Body, &d.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// WeeklyRecipientEmail devuelve el correo solo si el destinatario sigue siendo ADMIN activo y la
// empresa sigue activa con el correo semanal habilitado; si no, devuelve cadena vacía.
func (s *store) WeeklyRecipientEmail(ctx context.Context, org, user string) (string, error) {
	var email string
	err := s.tx.QueryRow(ctx, `SELECT u.email FROM organization_memberships m JOIN application_users u ON u.id=m.user_id AND u.status='ACTIVE'
 JOIN organizations o ON o.id=m.organization_id AND o.status='ACTIVE' LEFT JOIN report_settings rs ON rs.organization_id=o.id
 WHERE m.organization_id=$1 AND m.user_id=$2 AND m.status='ACTIVE' AND m.role='ADMIN' AND COALESCE(rs.weekly_email_enabled,TRUE)`, org, user).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return email, err
}

// FinishWeekly registra el resultado del intento y libera el lease.
func (s *store) FinishWeekly(ctx context.Context, d application.WeeklyDelivery, out application.WeeklyOutcome, now, retryAt time.Time) error {
	var next any
	if !retryAt.IsZero() {
		next = retryAt
	}
	_, err := s.tx.Exec(ctx, `UPDATE weekly_report_deliveries SET status=$2::text, provider_message_id=NULLIF($3,''), last_error_code=NULLIF($4,''),
 sent_at=CASE WHEN $2::text='SENT' THEN $5::timestamptz END, next_attempt_at=COALESCE($6::timestamptz,next_attempt_at), lease_token=NULL, locked_until=NULL, updated_at=$5
 WHERE id=$1::uuid AND status='PROCESSING'`, d.ID, out.Status, out.ProviderID, out.Code, now, next)
	return err
}
