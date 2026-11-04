package application

import (
	"context"
	"errors"
	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"strings"
	"testing"
	"time"
)

var validInput = domain.UploadInput{ID: "10000000-0000-0000-0000-000000000001", RunID: "20000000-0000-0000-0000-000000000001", ItemID: "30000000-0000-0000-0000-000000000001", OriginalName: "foto.png", MIMEType: "image/png", SizeBytes: 20, SHA256: strings.Repeat("a", 64)}

func newFixture() (*Service, *fakeStore, *fakeStorage) {
	db := &fakeStore{}
	storage := &fakeStorage{metadata: contracts.ObjectMetadata{ContentType: "image/png", ContentLength: 20, SHA256Hex: validInput.SHA256}}
	return New(db, storage, domain.Policy{MaxBytes: 1024, MaxFiles: 3, AllowedMIMEs: []string{"image/png"}}), db, storage
}
func TestReservationIsImmutableAndContentIsVerifiedBeforeConfirmation(t *testing.T) {
	s, db, storage := newFixture()
	ctx := context.Background()
	first, err := s.Reserve(ctx, "chef", "org", validInput)
	if err != nil || first.File.Status != "PENDING_UPLOAD" || first.Upload == nil {
		t.Fatal(first, err)
	}
	again, err := s.Reserve(ctx, "chef", "org", validInput)
	if err != nil || again.File.ID != first.File.ID || db.reservations != 1 {
		t.Fatal("reserva duplicada", again, err)
	}
	different := validInput
	different.SHA256 = strings.Repeat("b", 64)
	if _, err = s.Reserve(ctx, "chef", "org", different); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("modificó reserva", err)
	}
	storage.missing = true
	if _, err = s.Confirm(ctx, "chef", "org", first.File.ID); !errors.Is(err, domain.ErrNotReady) || db.file.Status != "PENDING_UPLOAD" {
		t.Fatal("confirmó objeto ausente", err)
	}
	storage.missing = false
	got, err := s.Confirm(ctx, "chef", "org", first.File.ID)
	if err != nil || got.Status != "AVAILABLE" || storage.inspections != 2 {
		t.Fatal(got, err)
	}
	before := storage.inspections
	if _, err = s.Confirm(ctx, "chef", "org", got.ID); err != nil || storage.inspections != before {
		t.Fatal("confirmación no idempotente", err)
	}
}
func TestBadBytesBecomeFailedAndNeverGetDownload(t *testing.T) {
	s, db, storage := newFixture()
	ctx := context.Background()
	if _, err := s.Reserve(ctx, "chef", "org", validInput); err != nil {
		t.Fatal(err)
	}
	storage.metadata.SHA256Hex = strings.Repeat("b", 64)
	if _, err := s.Confirm(ctx, "chef", "org", validInput.ID); !errors.Is(err, domain.ErrInvalidFile) || db.file.Status != "FAILED" {
		t.Fatal("aceptó bytes distintos", err, db.file)
	}
	if _, err := s.Download(ctx, "chef", "org", validInput.ID); !errors.Is(err, domain.ErrNotReady) || storage.downloads != 0 {
		t.Fatal("descarga de fallida", err)
	}
}
func TestValidationAndDeniedAccessCannotSignOrWriteObjects(t *testing.T) {
	s, db, storage := newFixture()
	ctx := context.Background()
	bad := validInput
	bad.MIMEType = "text/html"
	if _, err := s.Reserve(ctx, "chef", "org", bad); err == nil || db.transactions != 0 {
		t.Fatal("validación tardía", err)
	}
	db.denied = true
	if _, err := s.Reserve(ctx, "outsider", "org", validInput); !errors.Is(err, domain.ErrDenied) || storage.uploads != 0 || db.reservations != 0 {
		t.Fatal("firmó sin permiso", err)
	}
}
func TestPermissionIsRevalidatedAfterRemoteInspection(t *testing.T) {
	s, db, storage := newFixture()
	ctx := context.Background()
	if _, err := s.Reserve(ctx, "chef", "org", validInput); err != nil {
		t.Fatal(err)
	}
	storage.afterInspect = func() { db.denied = true }
	if _, err := s.Confirm(ctx, "chef", "org", validInput.ID); !errors.Is(err, domain.ErrDenied) || db.file.Status != "PENDING_UPLOAD" {
		t.Fatal("revocación ignorada", err)
	}
}
func TestDisabledStorageDoesNotCreateReservation(t *testing.T) {
	_, db, _ := newFixture()
	s := New(db, nil, domain.Policy{MaxBytes: 1024, MaxFiles: 3, AllowedMIMEs: []string{"image/png"}})
	if _, err := s.Reserve(context.Background(), "chef", "org", validInput); !errors.Is(err, domain.ErrUnavailable) || db.reservations != 0 {
		t.Fatal("reserva sin almacenamiento", err)
	}
}

type fakeStore struct {
	file                       domain.File
	denied                     bool
	transactions, reservations int
}

func (f *fakeStore) Within(ctx context.Context, fn func(Store) error) error {
	f.transactions++
	return fn(f)
}
func (f *fakeStore) Target(ctx context.Context, user, org, run, item string, write bool) (domain.Target, error) {
	if f.denied {
		return domain.Target{}, domain.ErrDenied
	}
	return domain.Target{OrganizationID: org, LocationID: "site", RunID: run, ItemID: item, UserID: user, Role: "CHEF", RunOwner: user, RunStatus: "DRAFT"}, nil
}
func (f *fakeStore) File(ctx context.Context, org, id string, write bool) (domain.File, error) {
	if f.denied {
		return domain.File{}, domain.ErrDenied
	}
	if f.file.ID != id {
		return domain.File{}, domain.ErrNotFound
	}
	return f.file, nil
}
func (f *fakeStore) List(context.Context, domain.Target) ([]domain.File, error) {
	return []domain.File{}, nil
}
func (f *fakeStore) Reserve(ctx context.Context, target domain.Target, file domain.File, max int) (domain.File, error) {
	if f.file.ID != "" {
		if e := f.file.Match(domain.UploadInput{ID: file.ID, RunID: file.RunID, ItemID: file.ItemID, IncidentID: file.IncidentID, ActionID: file.ActionID, Stage: file.Stage, OriginalName: file.OriginalName, MIMEType: file.MIMEType, SizeBytes: file.SizeBytes, SHA256: file.SHA256}); e != nil {
			return domain.File{}, e
		}
		return f.file, nil
	}
	f.file = file
	f.file.Status = "PENDING_UPLOAD"
	f.reservations++
	return f.file, nil
}
func (f *fakeStore) SetStatus(ctx context.Context, user string, file domain.File, status string) (domain.File, error) {
	f.file.Status = status
	return f.file, nil
}

type fakeStorage struct {
	metadata                        contracts.ObjectMetadata
	missing                         bool
	uploads, inspections, downloads int
	afterInspect                    func()
}

func (f *fakeStorage) PresignUpload(context.Context, contracts.UploadRequest) (contracts.SignedRequest, error) {
	f.uploads++
	return contracts.SignedRequest{URL: "https://test.r2.cloudflarestorage.com/private", ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (f *fakeStorage) PresignDownload(context.Context, string) (contracts.SignedRequest, error) {
	f.downloads++
	return contracts.SignedRequest{}, nil
}
func (f *fakeStorage) Head(context.Context, string) (contracts.ObjectMetadata, error) {
	return f.metadata, nil
}
func (f *fakeStorage) Inspect(context.Context, string) (contracts.ObjectMetadata, error) {
	f.inspections++
	if f.afterInspect != nil {
		f.afterInspect()
	}
	if f.missing {
		return contracts.ObjectMetadata{}, contracts.ErrObjectNotFound
	}
	return f.metadata, nil
}

func TestIncidentUploadUsesExplicitTargetAndReauthorizesConfirmation(t *testing.T) {
	s, db, storage := newFixture()
	in := validInput
	in.RunID = ""
	in.ItemID = ""
	in.IncidentID = validInput.RunID
	in.Stage = "BEFORE"
	reserved, err := s.Reserve(context.Background(), "chef", "org", in)
	if err != nil || reserved.File.IncidentID != in.IncidentID || reserved.File.RunID != "" {
		t.Fatal("destino incidente perdido", reserved, err)
	}
	if _, err = s.ListIncident(context.Background(), "chef", "org", in.IncidentID, "", "BEFORE"); err != nil {
		t.Fatal(err)
	}
	storage.afterInspect = func() { db.denied = true }
	if _, err = s.Confirm(context.Background(), "chef", "org", in.ID); !errors.Is(err, domain.ErrDenied) || db.file.Status != "PENDING_UPLOAD" {
		t.Fatal("confirmó luego de revocación", err)
	}
}
func (f *fakeStore) IncidentTarget(ctx context.Context, user, org, incident, action, stage string, write bool) (domain.Target, error) {
	if f.denied {
		return domain.Target{}, domain.ErrDenied
	}
	return domain.Target{OrganizationID: org, LocationID: "site", IncidentID: incident, ActionID: action, Stage: stage, UserID: user, Role: "CHEF", Reporter: user, IncidentStatus: "OPEN"}, nil
}

func TestPhysicalLocationMustMatchAuthorizedEvidenceTarget(t *testing.T) {
	s, db, _ := newFixture()
	if _, e := s.Reserve(context.Background(), "chef", "org", validInput); e != nil {
		t.Fatal(e)
	}
	db.file.LocationID = "another-site"
	db.file.Status = "AVAILABLE"
	if _, e := s.Download(context.Background(), "chef", "org", db.file.ID); !errors.Is(e, domain.ErrDenied) {
		t.Fatal("descargó foto física de otra sede", e)
	}
}
