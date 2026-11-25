package catalog

import (
	"strings"
	"testing"
)

func TestBancoOficial100Items(t *testing.T) {
	bank, _, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(bank.Items) != 100 {
		t.Fatalf("ítems = %d, se esperaban 100", len(bank.Items))
	}
	perModule := map[string]int{}
	for _, item := range bank.Items {
		perModule[item.Module]++
		if len(item.Options) != 5 {
			t.Errorf("%s tiene %d alternativas", item.Code, len(item.Options))
		}
		for _, letter := range []string{"A", "B", "C", "D", "E"} {
			if strings.TrimSpace(item.Options[letter]) == "" {
				t.Errorf("%s no tiene la alternativa %s", item.Code, letter)
			}
		}
		if item.KeyIndex() < 0 {
			t.Errorf("%s con clave fuera de A-E: %q", item.Code, item.Key)
		}
		// La justificación de la tabla de claves debe coincidir con la alternativa marcada como clave.
		if got := strings.TrimSuffix(item.Justification, "."); got != item.Options[item.Key] {
			t.Errorf("%s: la justificación %q no coincide con la alternativa %s %q", item.Code, got, item.Key, item.Options[item.Key])
		}
		if len(item.Sources) == 0 {
			t.Errorf("%s sin fuentes", item.Code)
		}
		// Ninguna alternativa repite texto: la aleatorización no puede producir dos claves idénticas.
		seen := map[string]bool{}
		for _, text := range item.OptionTexts() {
			if seen[text] {
				t.Errorf("%s repite una alternativa: %q", item.Code, text)
			}
			seen[text] = true
		}
	}
	for _, module := range []string{"M01", "M02", "M03", "M04", "M05"} {
		if perModule[module] != 20 {
			t.Errorf("%s tiene %d ítems, se esperaban 20", module, perModule[module])
		}
		recommended := bank.Recommended(module)
		if len(recommended) != 5 {
			t.Errorf("%s: selección recomendada de %d ítems", module, len(recommended))
		}
	}
	if bank.Version != 1 || bank.PassScorePercent != 80 || bank.ItemsPerEvaluation != 5 {
		t.Errorf("metadatos inesperados: versión %d, aprobación %d, ítems %d", bank.Version, bank.PassScorePercent, bank.ItemsPerEvaluation)
	}
}

func TestSeleccionRecomendadaDelDocumento(t *testing.T) {
	bank, _, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"M01": "M01-03,M01-05,M01-11,M01-18,M01-20",
		"M02": "M02-02,M02-08,M02-11,M02-16,M02-19",
		"M03": "M03-02,M03-04,M03-09,M03-13,M03-15",
		"M04": "M04-01,M04-10,M04-16,M04-18,M04-20",
		"M05": "M05-01,M05-05,M05-12,M05-18,M05-20",
	}
	for module, codes := range want {
		if got := strings.Join(bank.Recommended(module), ","); got != codes {
			t.Errorf("%s: %s, se esperaba %s", module, got, codes)
		}
	}
}

func TestFuentesDelDocumento(t *testing.T) {
	bank, _, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"P01", "P02", "P03", "P04", "P05", "P06", "P07", "P08", "C01", "C02", "C03", "C04", "G01", "I03", "I05"} {
		source, ok := bank.Source(code)
		if !ok || source.Short == "" || source.Title == "" {
			t.Errorf("falta la fuente %s", code)
		}
	}
	if s, _ := bank.Source("P01"); s.Short != "NTS 142 (P01)" || !strings.HasPrefix(s.URL, "https://www.digesa.minsa.gob.pe/") {
		t.Errorf("P01 = %+v", s)
	}
}

func TestRutaDeAprendizajeCubreElBanco(t *testing.T) {
	bank, courses, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(courses.Modules) != 5 {
		t.Fatalf("módulos = %d", len(courses.Modules))
	}
	total := 0
	for _, m := range courses.Modules {
		for _, u := range m.Units {
			total += len(u.Items)
		}
	}
	if total != len(bank.Items) {
		t.Errorf("las unidades cubren %d ítems de %d", total, len(bank.Items))
	}
}
