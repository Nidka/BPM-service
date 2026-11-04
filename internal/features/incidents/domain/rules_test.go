package domain

import (
	"errors"
	"testing"
	"time"
)

func TestTransitionsRequireResponsibleIndependentVerifierAndAvailableEvidence(t *testing.T) {
	now := time.Now().UTC()
	v := Incident{Status: "OPEN", RowVersion: 1, EvidenceRequired: true}
	a := Actor{ID: "chef", Role: "CHEF"}
	assign := Command{OperationID: "10000000-0000-0000-0000-000000000001", RowVersion: 1, ActionType: "ASSIGN", ActionID: "20000000-0000-0000-0000-000000000001", ResponsibleUserID: "worker", Description: "Corregir superficie", DueAt: now.Add(time.Hour)}
	if err := Apply(&v, a, assign, now); err != nil || v.Status != "ASSIGNED" || v.RowVersion != 2 || len(v.Actions) != 1 {
		t.Fatal(v, err)
	}
	if err := Apply(&v, a, Command{RowVersion: 2, ActionType: "START"}, now); !errors.Is(err, ErrDenied) {
		t.Fatal("otro actor inició", err)
	}
	worker := Actor{ID: "worker", Role: "WORKER"}
	if err := Apply(&v, worker, Command{RowVersion: 2, ActionType: "START"}, now); err != nil {
		t.Fatal(err)
	}
	if err := Apply(&v, worker, Command{RowVersion: 3, ActionType: "RESOLVE", Note: "Superficie corregida"}, now); err != nil {
		t.Fatal(err)
	}
	if err := Apply(&v, Actor{ID: "worker", Role: "CHEF"}, Command{RowVersion: 4, ActionType: "VERIFY", Note: "Revisado"}, now); !errors.Is(err, ErrDenied) {
		t.Fatal("autovalidó", err)
	}
	if err := Apply(&v, a, Command{RowVersion: 4, ActionType: "VERIFY", Note: "Revisado"}, now); !errors.Is(err, ErrConflict) {
		t.Fatal("cerró sin fotos", err)
	}
	v.BeforeCount = 1
	v.AfterCount = 1
	if err := Apply(&v, a, Command{RowVersion: 4, ActionType: "VERIFY", Note: "Revisado"}, now); err != nil || v.Status != "CLOSED" || v.Actions[0].Status != "VERIFIED" || v.RowVersion != 5 {
		t.Fatal(v, err)
	}
}
func TestMultipleActionsAndStaleVersionNeverTransition(t *testing.T) {
	v := Incident{Status: "IN_PROGRESS", RowVersion: 3, Actions: []Action{{ResponsibleUserID: "worker"}, {ResponsibleUserID: "worker"}}}
	if err := Apply(&v, Actor{ID: "worker", Role: "WORKER"}, Command{RowVersion: 3, ActionType: "RESOLVE", Note: "Listo"}, time.Now()); !errors.Is(err, ErrConflict) || v.RowVersion != 3 {
		t.Fatal(v, err)
	}
	v.Actions = v.Actions[:1]
	if err := Apply(&v, Actor{ID: "worker", Role: "WORKER"}, Command{RowVersion: 2, ActionType: "RESOLVE", Note: "Listo"}, time.Now()); !errors.Is(err, ErrConflict) || v.Status != "IN_PROGRESS" {
		t.Fatal(v, err)
	}
}
func TestNormalizeRejectsCommandFieldsAndInvalidCatalog(t *testing.T) {
	now := time.Now()
	for _, c := range []Command{{OperationID: "10000000-0000-0000-0000-000000000001", RowVersion: 1, ActionType: "START", Note: "extra"}, {OperationID: "10000000-0000-0000-0000-000000000001", RowVersion: 1, ActionType: "RESOLVE", Note: ""}} {
		if _, err := NormalizeCommand(c, now); err == nil {
			t.Fatal("aceptó campos", c)
		}
	}
	if _, err := NormalizeCatalog(Catalog{ID: "10000000-0000-0000-0000-000000000001", Code: "bad", Name: "Nombre", Area: "Área", NormativeCriterion: "Criterio", RiskLevel: "HIGH"}, true); err == nil {
		t.Fatal("aceptó código")
	}
}
