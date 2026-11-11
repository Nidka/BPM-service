package domain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCSVTextNeutralizaFormulas(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "+cmd": "'+cmd", "-1": "'-1", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx", "\rx": "'\rx", "normal": "normal", "": ""} {
		if got := CSVText(in); got != want {
			t.Fatalf("CSVText(%q)=%q, se esperaba %q", in, got, want)
		}
	}
	r := ExportRow{RunID: "r", LocationName: "=HYPERLINK(\"x\")", TemplateName: "@a", Area: "-b", SubmittedAt: time.Now(), LocalDate: time.Now(), TemplateVersion: 2, CompliancePercent: 87.5}
	rec := r.Record()
	if rec[4][0] != '\'' || rec[5][0] != '\'' || rec[6][0] != '\'' || rec[8] != "87.50" || len(rec) != len(ExportHeader) {
		t.Fatalf("registro inseguro: %v", rec)
	}
}

func TestParsePeriodLimites(t *testing.T) {
	if _, err := ParsePeriod("2026-01-01", "2026-04-02", MaxComplianceDays); err != nil {
		t.Fatal(err) // 92 días exactos
	}
	for _, c := range [][2]string{{"2026-01-01", "2026-04-03"}, {"2026-02-01", "2026-01-01"}, {"2026-13-01", "2026-01-01"}, {"", ""}, {"2026-1-1", "2026-01-02"}} {
		var v *ValidationError
		if _, err := ParsePeriod(c[0], c[1], MaxComplianceDays); !errors.As(err, &v) {
			t.Fatalf("%v debía ser inválido", c)
		}
	}
}

func TestBuildComplianceAgrupaPorDiaSedeYEmpresa(t *testing.T) {
	d1, d2 := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC)
	p := Period{From: d1, To: d2}
	rep := BuildCompliance(p, []DailyRow{
		{"a", "Sede A", d1, 2, 180}, {"b", "Sede B", d1, 1, 60}, {"a", "Sede A", d2, 1, 100},
	})
	if rep.Runs != 4 || *rep.Average != 85 || len(rep.Days) != 2 || *rep.Days[0].Average != 80 || len(rep.Locations) != 2 || *rep.Locations[0].Average != 93.33 {
		t.Fatalf("agregación inesperada: %+v", rep)
	}
	if empty := BuildCompliance(p, nil); empty.Average != nil || empty.Days == nil || empty.Locations == nil {
		t.Fatal("sin datos debe dar promedio nulo y listas vacías")
	}
}

func TestWeekToReport(t *testing.T) {
	// Lunes 2026-11-09 07:00 Lima: aún se reporta la semana anterior a la última completa.
	early := time.Date(2026, 11, 9, 7, 0, 0, 0, Lima)
	if w := WeekToReport(early); w.Start.Format(DateLayout) != "2026-10-26" || w.End.Format(DateLayout) != "2026-11-01" {
		t.Fatalf("semana temprana: %v", w)
	}
	late := time.Date(2026, 11, 9, 8, 0, 0, 0, Lima)
	if w := WeekToReport(late); w.Start.Format(DateLayout) != "2026-11-02" || w.End.Format(DateLayout) != "2026-11-08" {
		t.Fatalf("semana: %v", w)
	}
	if w := WeekToReport(time.Date(2026, 11, 15, 23, 0, 0, 0, Lima)); w.Start.Format(DateLayout) != "2026-11-02" {
		t.Fatalf("domingo: %v", w)
	}
}

func TestCertificadoReglas(t *testing.T) {
	ok, low := 80.0, 79.99
	if CheckEligibility(&ok, 80) != nil || !errors.Is(CheckEligibility(nil, 80), ErrNoData) {
		t.Fatal("el límite exacto aprueba y sin datos no")
	}
	var th *ThresholdError
	if !errors.As(CheckEligibility(&low, 80), &th) {
		t.Fatal("debía rechazar por umbral")
	}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if _, err := CertificatePeriod("2026-11-01", "2026-11-09", now); err != nil {
		t.Fatal(err)
	}
	if _, err := CertificatePeriod("2026-11-01", "2026-11-10", now); err == nil {
		t.Fatal("el período debe haber terminado")
	}
	a, _ := NewVerificationCode(nil)
	b, _ := NewVerificationCode(nil)
	if !ValidVerificationCode(a) || a == b {
		t.Fatal("código inválido o repetido")
	}
	if _, err := NewVerificationCode(bytes.NewReader([]byte("x"))); err == nil {
		t.Fatal("debe fallar si no hay entropía")
	}
	if ValidateSettings(Settings{ComplianceThreshold: 0}) == nil || ValidateSettings(Settings{ComplianceThreshold: 101}) == nil || ValidateSettings(Settings{ComplianceThreshold: 85.5}) != nil {
		t.Fatal("validación de umbral")
	}
}

func TestCorreoSemanalSinDatosPersonales(t *testing.T) {
	w := WeekToReport(time.Date(2026, 11, 10, 12, 0, 0, 0, Lima))
	avg := 91.5
	_, body := RenderWeeklyEmail("Mi Empresa", w, Summary{Runs: 3, Average: &avg, Locations: []LocationSummary{{LocationName: "Sede\nUno", Runs: 3, Average: &avg}}})
	if !strings.Contains(body, "91.50 %") || strings.Contains(body, "Sede\nUno") || !strings.Contains(body, "Sede Uno") {
		t.Fatal(body)
	}
}
