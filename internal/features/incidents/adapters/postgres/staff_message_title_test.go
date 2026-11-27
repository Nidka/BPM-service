package postgres

import "testing"

func TestStaffMessageTitleIncludesSender(t *testing.T) {
	if got := staffMessageTitle(" Lucía Ramos ", "Falta insumo"); got != "Aviso de Lucía Ramos: Falta insumo" {
		t.Fatalf("got %q", got)
	}
	if got := staffMessageTitle("", "Necesito ayuda"); got != "Aviso del equipo: Necesito ayuda" {
		t.Fatalf("got %q", got)
	}
}
