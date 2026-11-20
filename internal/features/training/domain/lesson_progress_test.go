package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestProgressWithLessonsAndPercent(t *testing.T) {
	order := []string{"a", "b", "c", "d"}
	cases := []struct {
		name      string
		status    string
		completed []string
		want      []string
		percent   int
	}{
		{"sin avance", ProgressNotStarted, nil, []string{}, 0},
		{"una de cuatro", ProgressInProgress, []string{"c"}, []string{"c"}, 25},
		{"respeta el orden del temario", ProgressInProgress, []string{"d", "a"}, []string{"a", "d"}, 50},
		{"ignora lecciones ajenas a la versión", ProgressInProgress, []string{"zzz", "b"}, []string{"b"}, 25},
		{"COMPLETED es 100 con todas completadas", ProgressCompleted, nil, order, 100},
	}
	for _, c := range cases {
		p := Progress{Status: c.status}.WithLessons(order, c.completed)
		if !reflect.DeepEqual(p.CompletedLessonIDs, c.want) || p.TotalLessons != 4 || p.CompletedLessons() != len(c.want) || p.Percent() != c.percent {
			t.Errorf("%s: %+v percent=%d", c.name, p, p.Percent())
		}
	}
	if got := (Progress{Status: ProgressInProgress}.WithLessons(nil, nil)).Percent(); got != 0 {
		t.Errorf("versión sin lecciones: %d", got)
	}
	if got := (Progress{Status: ProgressCompleted}).Percent(); got != 100 {
		t.Errorf("COMPLETED sin datos de lecciones: %d", got)
	}
	// 1 de 3 = 33 % y 2 de 3 = 67 % (redondeo de .5 hacia arriba).
	third := Progress{Status: ProgressInProgress}.WithLessons([]string{"a", "b", "c"}, []string{"a"})
	two := Progress{Status: ProgressInProgress}.WithLessons([]string{"a", "b", "c"}, []string{"a", "b"})
	if third.Percent() != 33 || two.Percent() != 67 {
		t.Errorf("redondeo: %d %d", third.Percent(), two.Percent())
	}
}

func draftWithKeyPoints(points []string) DraftInput {
	return DraftInput{PassingScore: 80, RowVersion: 1, Questions: []Question{},
		Lessons: []Lesson{{ID: "00000000-0000-4000-8000-000000000001", Title: "L", DisplayOrder: 1, KeyPoints: points}}}
}

func TestNormalizeDraftKeyPoints(t *testing.T) {
	out, err := NormalizeDraft(draftWithKeyPoints([]string{"  Uno ", "Dos"}))
	if err != nil || !reflect.DeepEqual(out.Lessons[0].KeyPoints, []string{"Uno", "Dos"}) {
		t.Fatalf("recorta espacios: %v %v", out.Lessons[0].KeyPoints, err)
	}
	// Ausente (nil) sigue siendo nil para que la aplicación conserve los previos; vacío sigue vacío.
	if out, err = NormalizeDraft(draftWithKeyPoints(nil)); err != nil || out.Lessons[0].KeyPoints != nil {
		t.Fatalf("nil debe permanecer nil: %#v %v", out.Lessons[0].KeyPoints, err)
	}
	if out, err = NormalizeDraft(draftWithKeyPoints([]string{})); err != nil || out.Lessons[0].KeyPoints == nil || len(out.Lessons[0].KeyPoints) != 0 {
		t.Fatalf("[] debe permanecer vacío no nulo: %#v %v", out.Lessons[0].KeyPoints, err)
	}
	// La entrada no se modifica.
	in := draftWithKeyPoints([]string{"  Uno "})
	if _, err = NormalizeDraft(in); err != nil || in.Lessons[0].KeyPoints[0] != "  Uno " {
		t.Fatalf("no debe mutar la entrada: %q", in.Lessons[0].KeyPoints)
	}

	max := strings.Repeat("ñ", MaxKeyPointLength)
	if _, err = NormalizeDraft(draftWithKeyPoints([]string{max, "b", "c", "d", "e"})); err != nil {
		t.Fatalf("5 puntos de 200 caracteres: %v", err)
	}
	invalid := map[string][]string{
		"seis puntos":    {"a", "b", "c", "d", "e", "f"},
		"vacío":          {""},
		"solo espacios":  {"ok", "   "},
		"201 caracteres": {max + "x"},
		"duplicado":      {"Lavar", "  LAVAR"},
		"salto de línea": {"uno\ndos"},
		"control":        {"uno\x00"},
		"utf8 inválido":  {"\xff\xfe"},
	}
	for name, points := range invalid {
		_, err = NormalizeDraft(draftWithKeyPoints(points))
		ve, ok := err.(*ValidationError)
		if !ok || ve.Fields["lessons[0].keyPoints"] == "" || len(ve.Fields) != 1 {
			t.Errorf("%s: %v", name, err)
		}
	}
}
