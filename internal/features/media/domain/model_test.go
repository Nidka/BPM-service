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
