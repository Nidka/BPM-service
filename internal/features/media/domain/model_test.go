package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestUploadPolicyAcceptsPhotoAndRejectsUntrustedMetadata(t *testing.T) {
	policy := Policy{MaxBytes: 10 << 20, MaxFiles: 3, AllowedMIMEs: []string{"image/jpeg", "image/png"}}
	input := UploadInput{ID: "10000000-0000-0000-0000-000000000001", RunID: "20000000-0000-0000-0000-000000000001", ItemID: "30000000-0000-0000-0000-000000000001", OriginalName: "foto.png", MIMEType: "image/png", SizeBytes: 200, SHA256: strings.Repeat("a", 64)}
	if _, err := policy.Validate(input); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*UploadInput){func(v *UploadInput) { v.ID = "../other" }, func(v *UploadInput) { v.OriginalName = "../secret.png" }, func(v *UploadInput) { v.MIMEType = "image/svg+xml" }, func(v *UploadInput) { v.SizeBytes = 11 << 20 }, func(v *UploadInput) { v.SHA256 = "fake" }, func(v *UploadInput) { v.OriginalName = "photo\n.png" }} {
		bad := input
		mutate(&bad)
		if _, err := policy.Validate(bad); err == nil {
			t.Fatal("aceptó metadata inválida", bad)
		}
	}
}
func TestAvailableFileCannotBeRedefinedByRetry(t *testing.T) {
	f := File{ID: "id", RunID: "run", ItemID: "item", OriginalName: "foto.png", MIMEType: "image/png", SizeBytes: 42, SHA256: "hash", Status: "AVAILABLE"}
	input := UploadInput{ID: "id", RunID: "run", ItemID: "item", OriginalName: "foto.png", MIMEType: "image/png", SizeBytes: 42, SHA256: "hash"}
	if err := f.Match(input); err != nil {
		t.Fatal(err)
	}
	input.SHA256 = "different"
	if !errors.Is(f.Match(input), ErrConflict) {
		t.Fatal("permitió redefinir la evidencia")
	}
}

func TestIncidentEvidenceHasOneExplicitTargetAndStage(t *testing.T) {
	p := Policy{MaxBytes: 1024, MaxFiles: 3, AllowedMIMEs: []string{"image/png"}}
	in := UploadInput{ID: "10000000-0000-0000-0000-000000000001", IncidentID: "20000000-0000-0000-0000-000000000001", Stage: "BEFORE", OriginalName: "antes.png", MIMEType: "image/png", SizeBytes: 20, SHA256: strings.Repeat("a", 64)}
	if _, err := p.Validate(in); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*UploadInput){func(v *UploadInput) { v.RunID = v.IncidentID; v.ItemID = v.IncidentID }, func(v *UploadInput) { v.Stage = "AFTER" }, func(v *UploadInput) { v.ActionID = v.ID }, func(v *UploadInput) { v.Stage = "GENERAL" }} {
		bad := in
		change(&bad)
		if _, err := p.Validate(bad); err == nil {
			t.Fatal("destino ambiguo aceptado", bad)
		}
	}
	in.Stage = "AFTER"
	in.ActionID = in.ID
	if _, err := p.Validate(in); err != nil {
		t.Fatal(err)
	}
	f := File{ID: in.ID, IncidentID: in.IncidentID, ActionID: in.ActionID, Stage: in.Stage, OriginalName: in.OriginalName, MIMEType: in.MIMEType, SizeBytes: in.SizeBytes, SHA256: in.SHA256}
	if err := f.Match(in); err != nil {
		t.Fatal(err)
	}
	in.ActionID = in.IncidentID
	if !errors.Is(f.Match(in), ErrConflict) {
		t.Fatal("reutilizó foto de otra acción")
	}
}
func TestIncidentEvidencePermissionsKeepBeforeAndAfterDistinct(t *testing.T) {
	target := Target{IncidentID: "incident", UserID: "worker", Role: "WORKER", Reporter: "worker", IncidentStatus: "OPEN", Stage: "BEFORE"}
	if !target.CanRead() || !target.CanWrite() {
		t.Fatal("reportero no puede documentar incidencia")
	}
	target.UserID = "other"
	if target.CanRead() || target.CanWrite() {
		t.Fatal("otro trabajador accedió")
	}
	target.UserID = "worker"
	target.Responsible = "worker"
	target.ActionStatus = "IN_PROGRESS"
	target.IncidentStatus = "IN_PROGRESS"
	target.Stage = "AFTER"
	if !target.CanWrite() {
		t.Fatal("responsable no puede documentar corrección")
	}
	target.UserID = "admin"
	target.Role = "ADMIN"
	if !target.CanRead() || target.CanWrite() {
		t.Fatal("supervisor fabricó evidencia de ejecutor")
	}
	target.Responsible = "admin"
	if target.CanWrite() {
		t.Fatal("administrador ejecutó corrección asignada antes del cambio de rol")
	}
	target.UserID = "worker"
	target.Role = "WORKER"
	target.Responsible = "worker"
	target.IncidentStatus = "RESOLVED"
	if target.CanWrite() {
		t.Fatal("cambió evidencia enviada a validación")
	}
}
