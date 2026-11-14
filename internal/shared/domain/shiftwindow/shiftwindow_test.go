package shiftwindow

import (
	"testing"
	"time"
)

func TestWindowValidationAndContainment(t *testing.T) {
	if _, err := New("06:00", "06:00"); err == nil {
		t.Fatal("aceptó turno de duración cero")
	}
	for _, bad := range []string{"6:00", "24:00", "06:60", "", "0600"} {
		if _, err := New(bad, "14:00"); err == nil {
			t.Fatal("aceptó hora inválida", bad)
		}
	}
	morning, _ := New("06:00", "14:00")
	night, _ := New("22:00", "06:00")
	if !night.Crosses() || morning.Crosses() {
		t.Fatal("cruce de medianoche mal detectado")
	}
	for minute, want := range map[int]bool{359: false, 360: true, 839: true, 840: false} {
		if morning.Contains(minute) != want {
			t.Fatal("mañana", minute)
		}
	}
	for minute, want := range map[int]bool{1319: false, 1320: true, 1439: true, 0: true, 359: true, 360: false} {
		if night.Contains(minute) != want {
			t.Fatal("noche", minute)
		}
	}
	afternoon, _ := New("14:00", "22:00")
	if morning.Overlaps(afternoon) || afternoon.Overlaps(night) || night.Overlaps(morning) {
		t.Fatal("turnos contiguos no deben solaparse")
	}
	late, _ := New("13:00", "15:00")
	if !late.Overlaps(morning) || !morning.Overlaps(late) {
		t.Fatal("no detectó solape")
	}
	over, _ := New("05:00", "23:00")
	if !over.Overlaps(night) {
		t.Fatal("no detectó solape con turno nocturno")
	}
}

func TestSelectUsesLocationZone(t *testing.T) {
	lima, _ := time.LoadLocation("America/Lima") // UTC-5 todo el año
	morning, _ := New("06:00", "14:00")
	night, _ := New("22:00", "06:00")
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	for _, tc := range []struct {
		name  string
		w     Window
		now   string
		start string
		end   string
		state State
	}{
		{"dentro de la mañana en zona Lima", morning, "2026-10-20T15:00:00Z", "2026-10-20T11:00:00Z", "2026-10-20T19:00:00Z", Open},
		{"el mismo instante UTC aún no abre en otra hora local", morning, "2026-10-20T10:59:00Z", "2026-10-20T11:00:00Z", "2026-10-20T19:00:00Z", Upcoming},
		{"fin excluyente", morning, "2026-10-20T19:00:00Z", "2026-10-20T11:00:00Z", "2026-10-20T19:00:00Z", Closed},
		{"noche tras medianoche local", night, "2026-10-21T08:00:00Z", "2026-10-21T03:00:00Z", "2026-10-21T11:00:00Z", Open},
		{"noche desde las 22:00", night, "2026-10-21T03:00:00Z", "2026-10-21T03:00:00Z", "2026-10-21T11:00:00Z", Open},
		{"noche fuera de horario muestra la próxima", night, "2026-10-21T16:00:00Z", "2026-10-22T03:00:00Z", "2026-10-22T11:00:00Z", Upcoming},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, st := tc.w.Select(at(tc.now), lima)
			if s.Format(time.RFC3339) != tc.start || e.Format(time.RFC3339) != tc.end || st != tc.state {
				t.Fatalf("got %s %s %s", s.Format(time.RFC3339), e.Format(time.RFC3339), st)
			}
		})
	}
}

func TestOccurrenceFollowsDaylightSaving(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	w, _ := New("01:00", "05:00") // 2026-03-08 salta de 02:00 a 03:00 locales.
	s, e := w.OccurrenceOn(ny, 2026, time.March, 8)
	if s.Format(time.RFC3339) != "2026-03-08T06:00:00Z" || e.Format(time.RFC3339) != "2026-03-08T09:00:00Z" {
		t.Fatal(s, e)
	}
}
