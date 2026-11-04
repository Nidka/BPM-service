package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRuleConfigurationClosedAndDecimal(t *testing.T) {
	for _, v := range []struct {
		kind, raw string
		ok        bool
	}{{"CHECKLIST_MISSING", `{"afterLocalTime":"23:59"}`, true}, {"CHECKLIST_MISSING", `{"afterLocalTime":"24:00"}`, false}, {"TEMPERATURE_DANGER", `{"minimumTemperatureCelsius":4,"maximumTemperatureCelsius":60}`, true}, {"TEMPERATURE_DANGER", `{"minimumTemperatureCelsius":4.001,"maximumTemperatureCelsius":60}`, false}, {"TEMPERATURE_DANGER", `{"minimumTemperatureCelsius":60,"maximumTemperatureCelsius":4}`, false}, {"ORGANIZATION_INACTIVE", `{"inactivityDays":365}`, true}, {"ORGANIZATION_INACTIVE", `{"inactivityDays":0}`, false}, {"TRAINING_ASSIGNED", `{}`, true}, {"TRAINING_ASSIGNED", `{"extra":1}`, false}, {"TRAINING_ASSIGNED", `null`, false}, {"CHECKLIST_MISSING", `{"afterLocalTime":"12:00","afterLocalTime":"13:00"}`, false}} {
		if e := ValidateConfiguration(v.kind, json.RawMessage(v.raw)); (e == nil) != v.ok {
			t.Fatalf("%s %s: %v", v.kind, v.raw, e)
		}
	}
}
func TestLocalDayDST(t *testing.T) {
	z, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		date  string
		hours int
	}{{"2026-03-08T16:00:00Z", 23}, {"2026-11-01T16:00:00Z", 25}} {
		now, _ := time.Parse(time.RFC3339, v.date)
		start, end := LocalDay(now, z)
		if end.Sub(start) != time.Duration(v.hours)*time.Hour {
			t.Fatal(start, end)
		}
	}
}

func TestRejectZeroUUID(t *testing.T) {
	if ValidID("00000000-0000-0000-0000-000000000000") {
		t.Fatal("zero UUID accepted")
	}
}
