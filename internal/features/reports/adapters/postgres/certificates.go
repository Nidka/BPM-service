package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

// Settings devuelve la configuración de la empresa o los valores por defecto.
func (s *store) Settings(ctx context.Context, org string) (domain.Settings, error) {
	out := domain.DefaultSettings()
	err := s.tx.QueryRow(ctx, `SELECT weekly_email_enabled, compliance_threshold::float8 FROM report_settings WHERE organization_id=$1`, org).Scan(&out.WeeklyEmailEnabled, &out.ComplianceThreshold)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DefaultSettings(), nil
	}
	return out, err
}

// SaveSettings crea o reemplaza la configuración.
func (s *store) SaveSettings(ctx context.Context, org, user string, v domain.Settings) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO report_settings(organization_id,weekly_email_enabled,compliance_threshold,created_by,updated_by) VALUES($1,$2,$3,$4,$4)
 ON CONFLICT (organization_id) DO UPDATE SET weekly_email_enabled=EXCLUDED.weekly_email_enabled, compliance_threshold=EXCLUDED.compliance_threshold, updated_by=EXCLUDED.updated_by, updated_at=NOW()`,
		org, v.WeeklyEmailEnabled, v.ComplianceThreshold, user)
	return err
}

// LockCertificates serializa la emisión por organización durante la transacción.
func (s *store) LockCertificates(ctx context.Context, org string) error {
	_, err := s.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('reports.certificates'), hashtext($1))`, org)
	return err
}

const certificateColumns = `id::text, period_start, period_end, average_compliance::float8, threshold::float8, runs_count, verification_code, issued_at`

func scanCertificate(row pgx.Row) (domain.Certificate, error) {
	var c domain.Certificate
	err := row.Scan(&c.ID, &c.PeriodStart, &c.PeriodEnd, &c.AverageCompliance, &c.Threshold, &c.Runs, &c.VerificationCode, &c.IssuedAt)
	return c, err
}

// FindCertificate devuelve la constancia ya emitida para el período exacto, si existe.
func (s *store) FindCertificate(ctx context.Context, org string, p domain.Period) (*domain.Certificate, error) {
	c, err := scanCertificate(s.tx.QueryRow(ctx, `SELECT `+certificateColumns+` FROM compliance_certificates WHERE organization_id=$1 AND period_start=$2 AND period_end=$3`, org, p.From, p.To))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// InsertCertificate guarda la constancia inmutable.
func (s *store) InsertCertificate(ctx context.Context, org, user string, p domain.Period, average, threshold float64, runs int, code string) (domain.Certificate, error) {
	return scanCertificate(s.tx.QueryRow(ctx, `INSERT INTO compliance_certificates(organization_id,period_start,period_end,average_compliance,threshold,runs_count,verification_code,issued_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+certificateColumns, org, p.From, p.To, average, threshold, runs, code, user))
}

// ListCertificates pagina por fecha de emisión descendente.
func (s *store) ListCertificates(ctx context.Context, org string, w pagination.Window) ([]domain.Certificate, error) {
	var anchor, id any
	if w.After.ID != "" {
		at, err := time.Parse(time.RFC3339Nano, w.After.Key)
		if err != nil {
			return nil, pagination.ErrInvalid
		}
		anchor, id = at, w.After.ID
	}
	rows, err := s.tx.Query(ctx, `SELECT `+certificateColumns+` FROM compliance_certificates WHERE organization_id=$1
 AND ($2::timestamptz IS NULL OR (issued_at,id)<($2::timestamptz,$3::uuid)) ORDER BY issued_at DESC, id DESC LIMIT $4`, org, anchor, id, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Certificate{}
	for rows.Next() {
		c, err := scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CertificateByCode es la verificación pública: solo devuelve datos agregados de la constancia.
func (r *Repository) CertificateByCode(ctx context.Context, code string) (domain.PublicCertificate, error) {
	var c domain.PublicCertificate
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(NULLIF(o.trade_name,''),o.legal_name), c.period_start, c.period_end, c.average_compliance::float8, c.threshold::float8, c.issued_at, c.verification_code
 FROM compliance_certificates c JOIN organizations o ON o.id=c.organization_id WHERE c.verification_code=$1`, code).
		Scan(&c.OrganizationName, &c.PeriodStart, &c.PeriodEnd, &c.AverageCompliance, &c.Threshold, &c.IssuedAt, &c.VerificationCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrCertificateNotFound
	}
	return c, err
}
