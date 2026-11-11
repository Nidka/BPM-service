package domain

import "testing"

func TestCleaningEvidenceRules(t *testing.T) {
	p := Policy{MaxBytes: 1000, MaxFiles: 3, AllowedMIMEs: []string{"image/jpeg", "image/png"}}
	good := UploadInput{ID: "10000000-0000-0000-0000-000000000001", CleaningID: "20000000-0000-0000-0000-000000000001", OriginalName: "a.jpg", MIMEType: "image/jpeg", SizeBytes: 10, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if _, e := p.Validate(good); e != nil {
		t.Fatal(e)
	}
	mixed := good
	mixed.RunID, mixed.ItemID = good.ID, good.ID
	if _, e := p.Validate(mixed); e == nil {
		t.Fatal("mezcló destinos")
	}
	pdf := good
	pdf.MIMEType = "application/pdf"
	if _, e := p.Validate(pdf); e == nil {
		t.Fatal("aceptó PDF")
	}
	own := Target{CleaningID: good.CleaningID, CleaningOwner: "w", UserID: "w", Role: "WORKER", CleaningOpen: true}
	if !own.CanRead() || !own.CanWrite() {
		t.Fatal("el responsable debe poder leer y escribir")
	}
	other := own
	other.UserID = "w2"
	if other.CanRead() || other.CanWrite() {
		t.Fatal("otro worker accedió")
	}
	admin := Target{CleaningID: good.CleaningID, CleaningOwner: "w", UserID: "a", Role: "ADMIN", CleaningOpen: true}
	if !admin.CanRead() || admin.CanWrite() {
		t.Fatal("el admin solo supervisa")
	}
	own.CleaningOpen = false
	if own.CanWrite() {
		t.Fatal("escribió fuera de la ventana")
	}
}
