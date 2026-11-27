package domain

import (
	"errors"
	"testing"
)

func TestNormalizeSupportContact(t *testing.T) {
	got, err := NormalizeSupportContact(" soporte@bpm.pe ", "+51 987-654-321")
	if err != nil || got.Email != "soporte@bpm.pe" || got.WhatsApp != "51987654321" {
		t.Fatalf("unexpected %+v %v", got, err)
	}
	got, err = NormalizeSupportContact("soporte@bpm.pe", "")
	if err != nil || got.WhatsApp != "" {
		t.Fatalf("whatsapp should be optional: %+v %v", got, err)
	}
	for _, tc := range []struct{ email, whatsapp, field string }{
		{"", "", "supportEmail"}, {"no-es-correo", "", "supportEmail"}, {"Soporte <a@b.pe>", "", "supportEmail"}, {"a@localhost", "", "supportEmail"},
		{"a@b.pe", "123", "supportWhatsapp"}, {"a@b.pe", "51abc987654", "supportWhatsapp"},
	} {
		_, err := NormalizeSupportContact(tc.email, tc.whatsapp)
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Fields[tc.field] == "" {
			t.Fatalf("%q/%q: expected %s error, got %v", tc.email, tc.whatsapp, tc.field, err)
		}
	}
}
