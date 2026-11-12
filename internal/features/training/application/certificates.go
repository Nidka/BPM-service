package application

import (
	"context"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// VerifyCertificate resuelve la verificación pública de una constancia sin
// autenticación. Un código con formato inválido y uno inexistente son
// indistinguibles (ErrCertificateNotFound); solo se devuelven datos mínimos.
func (s *Service) VerifyCertificate(ctx context.Context, code string) (out domain.PublicCertificate, err error) {
	if !domain.ValidVerificationCode(code) {
		return out, domain.ErrCertificateNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		var e error
		out, e = db.CertificateByCode(ctx, code)
		return e
	})
	return out, err
}
