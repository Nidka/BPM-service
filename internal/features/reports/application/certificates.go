package application

import (
	"context"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// IssueResult indica si la constancia se emitió ahora o ya existía para el período.
type IssueResult struct {
	Certificate domain.Certificate
	Created     bool
}

// IssueCertificate es el RF-39. El servidor calcula el cumplimiento promedio de toda la empresa
// en el período y emite solo si alcanza el umbral. Es idempotente por período: repetir la
// solicitud devuelve la constancia ya emitida, que nunca se modifica.
func (s *Service) IssueCertificate(ctx context.Context, user, org, from, to string) (IssueResult, error) {
	var out IssueResult
	err := s.work.Within(ctx, func(db Store) error {
		if _, err := s.authorize(ctx, db, user, org, canAdminister); err != nil {
			return err
		}
		period, err := domain.CertificatePeriod(from, to, s.now())
		if err != nil {
			return err
		}
		if err = db.LockCertificates(ctx, org); err != nil {
			return err
		}
		existing, err := db.FindCertificate(ctx, org, period)
		if err != nil {
			return err
		}
		if existing != nil {
			out = IssueResult{Certificate: *existing}
			return nil
		}
		settings, err := db.Settings(ctx, org)
		if err != nil {
			return err
		}
		scope := domain.Scope{OrganizationID: org, UserID: user, AllLocations: true, Period: period}
		rows, err := db.ComplianceRows(ctx, scope)
		if err != nil {
			return err
		}
		report := domain.BuildCompliance(period, rows)
		if err = domain.CheckEligibility(report.Average, settings.ComplianceThreshold); err != nil {
			return err
		}
		code, err := domain.NewVerificationCode(s.random)
		if err != nil {
			return err
		}
		cert, err := db.InsertCertificate(ctx, org, user, period, *report.Average, settings.ComplianceThreshold, report.Runs, code)
		if err != nil {
			return err
		}
		if err = db.Audit(ctx, org, user, "COMPLIANCE_CERTIFICATE_ISSUED", "COMPLIANCE_CERTIFICATE", cert.ID); err != nil {
			return err
		}
		out = IssueResult{Certificate: cert, Created: true}
		return nil
	})
	return out, err
}

// ListCertificates lista las constancias de la empresa, más recientes primero.
func (s *Service) ListCertificates(ctx context.Context, user, org string, page pagination.Request) (pagination.Page[domain.Certificate], error) {
	var out pagination.Page[domain.Certificate]
	err := s.work.Within(ctx, func(db Store) error {
		if _, err := s.authorize(ctx, db, user, org, canAdminister); err != nil {
			return err
		}
		w, err := pagination.Open(page, user, org, "reports.certificates")
		if err != nil {
			return err
		}
		rows, err := db.ListCertificates(ctx, org, w)
		if err != nil {
			return err
		}
		out = pagination.Close(rows, w, func(v domain.Certificate) pagination.Position {
			return pagination.Position{Key: v.IssuedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), ID: v.ID}
		}, user, org, "reports.certificates")
		return nil
	})
	return out, err
}

// VerifyCertificate consulta una constancia por código; un código con formato inválido es 404.
func (s *Service) VerifyCertificate(ctx context.Context, code string) (domain.PublicCertificate, error) {
	if !domain.ValidVerificationCode(code) {
		return domain.PublicCertificate{}, domain.ErrCertificateNotFound
	}
	return s.verifier.CertificateByCode(ctx, code)
}
