package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// VideoReservation es la reserva de un video y, si aún está pendiente, su PUT firmado.
type VideoReservation struct {
	File   domain.VideoFile
	Upload *contracts.SignedRequest
}

// ReserveVideo guarda los metadatos inmutables y firma un PUT sin sobrescritura.
// Repetir los mismos metadatos devuelve la misma reserva con una firma nueva;
// metadatos distintos para el mismo id son ErrVideoConflict.
func (s *Service) ReserveVideo(ctx context.Context, user, org string, in domain.VideoUploadInput) (out VideoReservation, err error) {
	if !domain.ValidOrganization(org) {
		return out, domain.ErrCourseNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		// La autorización precede a la validación de formato.
		valid, e := s.policy.Validate(in)
		if e != nil {
			return e
		}
		if s.videos == nil {
			return domain.ErrUnavailable
		}
		file := domain.VideoFile{ID: valid.ID, OrganizationID: org, OriginalName: valid.OriginalName, MIMEType: valid.MIMEType,
			SizeBytes: valid.SizeBytes, SHA256: valid.SHA256, CreatedBy: user, ObjectKey: domain.VideoObjectKey(org, valid.ID, valid.MIMEType)}
		out.File, e = db.ReserveVideo(ctx, org, user, file)
		return e
	})
	if err != nil {
		return out, err
	}
	if out.File.Status == "AVAILABLE" {
		return out, nil
	}
	if out.File.Status != "PENDING_UPLOAD" {
		return out, domain.ErrVideoConflict
	}
	ticket, err := s.videos.PresignVideoUpload(ctx, contracts.UploadRequest{ObjectKey: out.File.ObjectKey, ContentType: out.File.MIMEType,
		ContentLength: out.File.SizeBytes, SHA256Hex: out.File.SHA256})
	if err != nil {
		return out, domain.ErrUnavailable
	}
	out.Upload = &ticket
	return out, nil
}

// videoInspectBudget acota la verificación completa del objeto (HEAD, firma de
// formato y SHA-256 en streaming). Se separa del plazo de la solicitud HTTP para
// que videos grandes terminen de verificarse, pero nunca de forma indefinida.
const videoInspectBudget = 2 * time.Minute

// ConfirmVideo verifica el objeto cargado: tamaño, tipo, firma real del formato y
// SHA-256 de todos los bytes. Si coincide pasa a AVAILABLE; si no, a FAILED.
func (s *Service) ConfirmVideo(ctx context.Context, user, org, id string) (domain.VideoFile, error) {
	id = strings.ToLower(id)
	if !domain.ValidOrganization(org) || !domain.ValidID(id) {
		return domain.VideoFile{}, domain.ErrVideoNotFound
	}
	file, err := s.loadVideo(ctx, user, org, id)
	if err != nil {
		return file, err
	}
	if file.Status == "AVAILABLE" {
		return file, nil
	}
	if file.Status != "PENDING_UPLOAD" {
		return file, domain.ErrVideoConflict
	}
	if s.videos == nil {
		return file, domain.ErrUnavailable
	}
	inspectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), videoInspectBudget)
	defer cancel()
	inspection, err := s.videos.InspectVideo(inspectCtx, file.ObjectKey)
	if errors.Is(err, contracts.ErrObjectNotFound) {
		return file, domain.ErrVideoNotReady
	}
	invalid := errors.Is(err, contracts.ErrInvalidObject)
	if err != nil && !invalid {
		return file, domain.ErrUnavailable
	}
	invalid = invalid || inspection.ContentType != file.MIMEType || inspection.ContentLength != file.SizeBytes ||
		inspection.SHA256Hex != file.SHA256 || !domain.ValidVideoSignature(file.MIMEType, inspection.Header)
	status := "AVAILABLE"
	if invalid {
		status = "FAILED"
	}
	var confirmed domain.VideoFile
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		current, e := db.Video(ctx, org, id, true)
		if e != nil {
			return e
		}
		if current.Status == "AVAILABLE" {
			confirmed = current
			return nil
		}
		if current.Status != "PENDING_UPLOAD" || current.ObjectKey != file.ObjectKey {
			return domain.ErrVideoConflict
		}
		confirmed, e = db.SetVideoStatus(ctx, org, user, current, status)
		return e
	})
	if err != nil {
		return file, err
	}
	if confirmed.Status == "FAILED" {
		return confirmed, domain.ErrVideoInvalid
	}
	return confirmed, nil
}

func (s *Service) loadVideo(ctx context.Context, user, org, id string) (file domain.VideoFile, err error) {
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		var e error
		file, e = db.Video(ctx, org, id, false)
		return e
	})
	return file, err
}
