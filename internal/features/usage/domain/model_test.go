package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeBatchLimitesYTipos(t *testing.T) {
	now := time.Now().UTC()
	id := func(n int) string {
		return "00000000-0000-4000-8000-" + string(rune('0'+n/10)) + string(rune('0'+n%10)) + "0000000000"
	}
	ok := Event{ID: id(1), SessionID: id(2), Type: ModuleVisit, Module: "TRAINING", OccurredAt: now}
	if out, err := NormalizeBatch([]Event{ok}, now); err != nil || out[0].Module != "TRAINING" {
		t.Fatal(err)
	}
	var v *ValidationError
	if _, err := NormalizeBatch(nil, now); !errors.As(err, &v) {
		t.Fatal("lote vacío")
	}
	big := make([]Event, MaxBatchEvents+1)
	if _, err := NormalizeBatch(big, now); !errors.As(err, &v) || v.Fields["events"] == "" {
		t.Fatal("lote grande")
	}
	dup := []Event{ok, ok}
	if _, err := NormalizeBatch(dup, now); !errors.As(err, &v) {
		t.Fatal("UUID repetido en el lote")
	}
	neg := -1
	for name, e := range map[string]Event{
		"módulo":     {ID: id(3), SessionID: id(2), Type: ModuleVisit, Module: "CAJA", OccurredAt: now},
		"sin módulo": {ID: id(3), SessionID: id(2), Type: ModuleVisit, OccurredAt: now},
		"futuro":     {ID: id(3), SessionID: id(2), Type: SessionStart, OccurredAt: now.Add(time.Hour)},
		"duración":   {ID: id(3), SessionID: id(2), Type: SessionStart, OccurredAt: now, DurationSeconds: &neg},
		"uuid":       {ID: "x", SessionID: id(2), Type: SessionStart, OccurredAt: now},
	} {
		if _, err := NormalizeBatch([]Event{e}, now); !errors.As(err, &v) {
			t.Fatalf("%s debía rechazarse", name)
		}
	}
	if (Actor{Role: "SUPER_ADMIN"}).CanRecord() || !(Actor{Role: "SUPER_ADMIN"}).CanQuery() || (Actor{Role: "WORKER"}).CanQuery() {
		t.Fatal("permisos")
	}
}
