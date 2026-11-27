package domain

import "testing"

func TestFlaggedCountsFailuresAndObservations(t *testing.T) {
	lo, hi, cold, hot := 0.0, 5.0, 3.0, 9.0
	v := Run{Items: []Item{{ID: "a", ResponseType: "COMPLIANCE"}, {ID: "b", ResponseType: "TEMPERATURE", MinimumValue: &lo, MaximumValue: &hi}, {ID: "c", ResponseType: "TEMPERATURE", MinimumValue: &lo, MaximumValue: &hi}},
		Answers: []Answer{{ItemID: "a", ComplianceResult: "NON_COMPLIANT", Observation: "Piso mojado"}, {ItemID: "b", NumericValue: &hot}, {ItemID: "c", NumericValue: &cold}}}
	f, o := Flagged(v)
	if f != 2 || o != 1 {
		t.Fatal(f, o)
	}
	title, msg := FlaggedText("Limpieza", "Principal", f, o)
	if title != "Control con «No cumple»: Limpieza" || msg != "Principal · 2 no cumplen · 1 observación" {
		t.Fatal(title, msg)
	}
	if title, msg = FlaggedText("Limpieza", "Principal", 0, 12); title != "Control con observaciones: Limpieza" || msg != "Principal · 12 observaciones" {
		t.Fatal(title, msg)
	}
}
