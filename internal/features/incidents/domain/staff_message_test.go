package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeStaffMessage(t *testing.T) {
	ok := StaffMessage{ID: "10000000-0000-0000-0000-000000000001", LocationID: "20000000-0000-0000-0000-000000000001", Kind: "supply", Text: "  Falta aceite "}
	v, e := NormalizeStaffMessage(ok)
	if e != nil || v.Kind != "SUPPLY" || v.Text != "Falta aceite" || StaffMessageLabel(v.Kind) != "Falta insumo" {
		t.Fatal(v, e)
	}
	for _, bad := range []StaffMessage{{ID: "x", LocationID: ok.LocationID, Kind: "HELP"}, {ID: ok.ID, LocationID: ok.LocationID, Kind: "OTRO"},
		{ID: ok.ID, LocationID: ok.LocationID, Kind: "OTHER"}, {ID: ok.ID, LocationID: ok.LocationID, Kind: "HELP", Text: strings.Repeat("a", 501)}} {
		var invalid *ValidationError
		if _, e := NormalizeStaffMessage(bad); !errors.As(e, &invalid) {
			t.Fatal("debe rechazar", bad)
		}
	}
}
