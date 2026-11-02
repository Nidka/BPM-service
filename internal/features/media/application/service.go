package application

import (
	"context"
	"errors"
	"strings"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
)

type Store interface {
	Target(context.Context, string, string, string, string, bool) (domain.Target, error)
	File(context.Context, string, string, bool) (domain.File, error)
	List(context.Context, domain.Target) ([]domain.File, error)
	Reserve(context.Context, domain.Target, domain.File, int) (domain.File, error)
	SetStatus(context.Context, string, domain.File, string) (domain.File, error)
}
type UnitOfWork interface {
	Within(context.Context, func(Store) error) error
}
type Service struct {
	work    UnitOfWork
	objects contracts.ObjectStorage
	policy  domain.Policy
}
type Reservation struct {
	File   domain.File
	Upload *contracts.SignedRequest
}
type Collection struct {
	Files           []domain.File
	Policy          domain.Policy
	UploadAvailable bool
}

func New(work UnitOfWork, objects contracts.ObjectStorage, policy domain.Policy) *Service {
	return &Service{work: work, objects: objects, policy: policy}
}

func (s *Service) List(ctx context.Context, user, org, run, item string) (out Collection, err error) {
	if !domain.ValidID(run) || !domain.ValidID(item) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		target, e := targetAccess(ctx, db, user, org, run, item, false)
		if e != nil {
			return e
		}
		out.Files, e = db.List(ctx, target)
		out.Policy, out.UploadAvailable = s.policy, s.objects != nil
		return e
	})
	return
}

func (s *Service) Reserve(ctx context.Context, user, org string, input domain.UploadInput) (out Reservation, err error) {
	input, err = s.policy.Validate(input)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		target, e := targetAccess(ctx, db, user, org, input.RunID, input.ItemID, true)
		if e != nil {
			return e
		}
		if s.objects == nil {
			return domain.ErrUnavailable
		}
		ext := ".jpg"
		if input.MIMEType == "image/png" {
			ext = ".png"
		}
		file := domain.File{ID: input.ID, OrganizationID: target.OrganizationID, LocationID: target.LocationID,
			RunID: target.RunID, ItemID: target.ItemID, OriginalName: input.OriginalName, MIMEType: input.MIMEType,
			SizeBytes: input.SizeBytes, SHA256: input.SHA256, CreatedBy: user,
			ObjectKey: "organizations/" + target.OrganizationID + "/locations/" + target.LocationID + "/media/" + input.ID + ext}
		out.File, e = db.Reserve(ctx, target, file, s.policy.MaxFiles)
		return e
	})
	if err != nil {
		return out, err
	}
	if out.File.Status == "AVAILABLE" {
		return out, nil
	}
	if out.File.Status != "PENDING_UPLOAD" {
		return out, domain.ErrConflict
	}
	ticket, err := s.objects.PresignUpload(ctx, contracts.UploadRequest{ObjectKey: out.File.ObjectKey, ContentType: out.File.MIMEType, ContentLength: out.File.SizeBytes, SHA256Hex: out.File.SHA256})
	if err != nil {
		return out, domain.ErrUnavailable
	}
	out.Upload = &ticket
	return out, nil
}

func (s *Service) read(ctx context.Context, user, org, id string, write bool) (out domain.File, err error) {
	if !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		var e error
		out, e = fileAccess(ctx, db, user, org, strings.ToLower(id), write)
		return e
	})
	return
}

func (s *Service) Confirm(ctx context.Context, user, org, id string) (domain.File, error) {
	file, err := s.read(ctx, user, org, id, true)
	if err != nil {
		return file, err
	}
	if file.Status == "AVAILABLE" {
		return file, nil
	}
	if file.Status != "PENDING_UPLOAD" {
		return file, domain.ErrConflict
	}
	if s.objects == nil {
		return file, domain.ErrUnavailable
	}
	metadata, err := s.objects.Inspect(ctx, file.ObjectKey)
	if errors.Is(err, contracts.ErrObjectNotFound) {
		return file, domain.ErrNotReady
	}
	invalid := errors.Is(err, contracts.ErrInvalidObject)
	if err != nil && !invalid {
		return file, domain.ErrUnavailable
	}
	invalid = invalid || metadata.ContentType != file.MIMEType || metadata.ContentLength != file.SizeBytes || metadata.SHA256Hex != file.SHA256
	status := "AVAILABLE"
	if invalid {
		status = "FAILED"
	}
	var confirmed domain.File
	err = s.work.Within(ctx, func(db Store) error {
		current, e := fileAccess(ctx, db, user, org, id, true)
		if e != nil {
			return e
		}
		if current.Status == "AVAILABLE" {
			confirmed = current
			return nil
		}
		if current.Status != "PENDING_UPLOAD" || current.ObjectKey != file.ObjectKey {
			return domain.ErrConflict
		}
		confirmed, e = db.SetStatus(ctx, user, current, status)
		return e
	})
	if err != nil {
		return file, err
	}
	if confirmed.Status == "FAILED" {
		return confirmed, domain.ErrInvalidFile
	}
	return confirmed, nil
}

func (s *Service) Download(ctx context.Context, user, org, id string) (contracts.SignedRequest, error) {
	file, err := s.read(ctx, user, org, id, false)
	if err != nil {
		return contracts.SignedRequest{}, err
	}
	if file.Status != "AVAILABLE" {
		return contracts.SignedRequest{}, domain.ErrNotReady
	}
	if s.objects == nil {
		return contracts.SignedRequest{}, domain.ErrUnavailable
	}
	ticket, err := s.objects.PresignDownload(ctx, file.ObjectKey)
	if err != nil {
		return ticket, domain.ErrUnavailable
	}
	return ticket, nil
}

func (s *Service) Remove(ctx context.Context, user, org, id string) (out domain.File, err error) {
	if !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		current, e := fileAccess(ctx, db, user, org, id, true)
		if e != nil {
			return e
		}
		if current.Status == "DELETED" {
			out = current
			return nil
		}
		out, e = db.SetStatus(ctx, user, current, "DELETED")
		return e
	})
	return
}

func targetAccess(ctx context.Context, db Store, user, org, run, item string, write bool) (domain.Target, error) {
	target, err := db.Target(ctx, user, org, run, item, write)
	if err != nil {
		return target, err
	}
	if !target.CanRead() || write && !target.CanWrite() {
		return domain.Target{}, domain.ErrDenied
	}
	return target, nil
}
func fileAccess(ctx context.Context, db Store, user, org, id string, write bool) (domain.File, error) {
	file, err := db.File(ctx, org, id, false)
	if err != nil {
		return file, err
	}
	if _, err = targetAccess(ctx, db, user, org, file.RunID, file.ItemID, write); err != nil {
		return domain.File{}, err
	}
	// La lectura posterior al bloqueo de la revisión observa el estado vigente.
	return db.File(ctx, org, id, write)
}
