package calendarperiod

import (
	"testing"
	"time"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	z, e := time.LoadLocation(name)
	if e != nil {
		t.Fatal(e)
	}
	return z
}

func TestBoundsByFrequencyZoneAndDST(t *testing.T) {
	utc := func(v string) time.Time {
		t.Helper()
		p, e := time.Parse(time.RFC3339, v)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	for _, tc := range []struct {
		name, frequency, zone, now, start, end string
	}{
		{"día local de Lima", "DAILY", "America/Lima", "2026-11-20T15:00:00Z", "2026-11-20T05:00:00Z", "2026-11-21T05:00:00Z"},
		{"la noche de Lima sigue en el día anterior", "DAILY", "America/Lima", "2026-11-20T04:59:59Z", "2026-11-19T05:00:00Z", "2026-11-20T05:00:00Z"},
		{"el inicio del día es inclusivo", "DAILY", "America/Lima", "2026-11-20T05:00:00Z", "2026-11-20T05:00:00Z", "2026-11-21T05:00:00Z"},
		{"zona adelantada: Tokio ya está en el día siguiente", "DAILY", "Asia/Tokyo", "2026-11-19T16:00:00Z", "2026-11-19T15:00:00Z", "2026-11-20T15:00:00Z"},
		{"semana desde el lunes (viernes)", "WEEKLY", "America/Lima", "2026-11-20T15:00:00Z", "2026-11-16T05:00:00Z", "2026-11-23T05:00:00Z"},
		{"semana desde el lunes (domingo cierra la semana)", "WEEKLY", "America/Lima", "2026-11-22T20:00:00Z", "2026-11-16T05:00:00Z", "2026-11-23T05:00:00Z"},
		{"semana desde el lunes (lunes local empieza otra)", "WEEKLY", "America/Lima", "2026-11-23T05:00:00Z", "2026-11-23T05:00:00Z", "2026-11-30T05:00:00Z"},
		{"mes calendario", "MONTHLY", "America/Lima", "2026-11-20T15:00:00Z", "2026-11-01T05:00:00Z", "2026-12-01T05:00:00Z"},
		{"fin de año en la zona de la sede", "MONTHLY", "America/Lima", "2027-01-01T04:30:00Z", "2026-12-01T05:00:00Z", "2027-01-01T05:00:00Z"},
		{"cambio de horario: día de 25 horas", "DAILY", "America/New_York", "2026-11-01T12:00:00Z", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		{"cambio de horario: día de 23 horas", "DAILY", "America/New_York", "2026-03-08T12:00:00Z", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z"},
		{"cambio de horario dentro de la semana", "WEEKLY", "America/New_York", "2026-03-08T12:00:00Z", "2026-03-02T05:00:00Z", "2026-03-09T04:00:00Z"},
		{"cambio de horario dentro del mes", "MONTHLY", "America/New_York", "2026-03-20T12:00:00Z", "2026-03-01T05:00:00Z", "2026-04-01T04:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end, e := Bounds(tc.frequency, utc(tc.now), zone(t, tc.zone))
			if e != nil || !start.Equal(utc(tc.start)) || !end.Equal(utc(tc.end)) || start.Location() != time.UTC || end.Location() != time.UTC {
				t.Fatalf("%v %v %v", start, end, e)
			}
		})
	}
	if _, _, e := Bounds("ONCE", time.Now(), time.UTC); e != ErrFrequency {
		t.Fatal(e)
	}
}
