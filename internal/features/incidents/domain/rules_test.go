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

// El administrador opera como CHEF, pero nunca valida su propia corrección.
func TestAdminReportsAndResolvesButNeverVerifiesOwnCorrection(t *testing.T) {
	now := time.Now().UTC()
	admin := Actor{ID: "admin", Role: "ADMIN"}
	other := Actor{ID: "otro", Role: "ADMIN"}
	if !CanReport("ADMIN") || CanReport("SUPER_ADMIN") {
		t.Fatal("el ADMIN reporta; SUPER_ADMIN no")
	}
	v := Incident{Status: "ASSIGNED", RowVersion: 1, EvidenceRequired: false, Actions: []Action{{ResponsibleUserID: "admin", Status: "PENDING"}}}
	if err := Apply(&v, admin, Command{RowVersion: 1, ActionType: "START"}, now); err != nil {
		t.Fatal(err)
	}
	if err := Apply(&v, admin, Command{RowVersion: 2, ActionType: "RESOLVE", Note: "Corregido"}, now); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"VERIFY", "RETURN"} {
		if err := Apply(&v, admin, Command{RowVersion: 3, ActionType: c, Note: "ok"}, now); !errors.Is(err, ErrDenied) {
			t.Fatal("el responsable validó su propia corrección", c, err)
		}
	}
	if got := Allowed(v, admin); len(got) != 0 {
		t.Fatal("opciones propias inesperadas", got)
	}
	if err := Apply(&v, other, Command{RowVersion: 3, ActionType: "VERIFY", Note: "ok"}, now); err != nil || v.Status != "CLOSED" {
		t.Fatal("otro administrador debe poder validar", err)
	}
	// Mismo ADMIN: valida una acción ajena aunque tenga otra propia.
	w := Incident{Status: "RESOLVED", RowVersion: 3, Actions: []Action{{ResponsibleUserID: "chef", Status: "RESOLVED"}}}
	if err := Apply(&w, admin, Command{RowVersion: 3, ActionType: "VERIFY", Note: "ok"}, now); err != nil {
		t.Fatal(err)
	}
}
