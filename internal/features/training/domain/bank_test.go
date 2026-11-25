package domain

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// bankPool arma 20 ítems del módulo M01 con la misma distribución de Bloom que el banco oficial.
func bankPool() []Question {
	levels := map[int]string{}
	spec := []struct {
		level string
		codes []int
	}{
		{BloomComprender, []int{1, 3, 7, 10, 16}}, {BloomRecordar, []int{2, 15}},
		{BloomAplicar, []int{4, 5, 6, 9, 11, 12, 17, 19}}, {BloomAnalizar, []int{8, 13, 18}}, {BloomEvaluar, []int{14, 20}},
	}
	for _, s := range spec {
		for _, c := range s.codes {
			levels[c] = s.level
		}
	}
	pool := make([]Question, 0, 20)
	for n := 1; n <= 20; n++ {
		code := fmt.Sprintf("M01-%02d", n)
		pool = append(pool, Question{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n), Text: "Pregunta " + code,
			Options: []string{"A " + code, "B " + code, "C " + code, "D " + code, "E " + code}, CorrectOption: n % 5,
			Explanation: "Justificación " + code, ItemCode: code, BankVersion: 1, Bloom: levels[n], Sources: []string{"P01"}, DisplayOrder: n})
	}
	return pool
}

var recommendedM01 = []string{"M01-03", "M01-05", "M01-11", "M01-18", "M01-20"}

func codes(items []DrawItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ItemCode
	}
	return out
}

func TestPrimerSorteoUsaLaSeleccionRecomendada(t *testing.T) {
	items := DrawEvaluation(bankPool(), recommendedM01, nil, rand.New(rand.NewSource(1)))
	got := codes(items)
	sort.Strings(got)
	for i, code := range recommendedM01 {
		if got[i] != code {
			t.Fatalf("sorteo %v, se esperaba %v", got, recommendedM01)
		}
	}
	for _, it := range items {
		if len(it.OptionOrder) != 5 {
			t.Fatalf("orden de alternativas %v", it.OptionOrder)
		}
		seen := map[int]bool{}
		for _, o := range it.OptionOrder {
			seen[o] = true
		}
		if len(seen) != 5 {
			t.Fatalf("el orden %v no es una permutación", it.OptionOrder)
		}
	}
}

func TestRotacionEvitaRepetirYBalanceaBloom(t *testing.T) {
	pool := bankPool()
	byCode := map[string]Question{}
	for _, q := range pool {
		byCode[q.ItemCode] = q
	}
	history := []ItemUse{}
	for _, code := range recommendedM01 {
		history = append(history, ItemUse{ItemCode: code, Uses: 1, LastUsed: time.Unix(100, 0)})
	}
	for seed := int64(0); seed < 50; seed++ {
		items := DrawEvaluation(pool, recommendedM01, history, rand.New(rand.NewSource(seed)))
		if len(items) != 5 {
			t.Fatalf("ítems = %d", len(items))
		}
		levels := map[string]int{}
		seen := map[string]bool{}
		for _, it := range items {
			if seen[it.ItemCode] {
				t.Fatalf("ítem repetido %s", it.ItemCode)
			}
			seen[it.ItemCode] = true
			for _, used := range recommendedM01 {
				if it.ItemCode == used {
					t.Fatalf("seed %d: reutilizó %s teniendo ítems sin ver", seed, used)
				}
			}
			levels[byCode[it.ItemCode].Bloom]++
		}
		if levels[BloomAplicar] != 2 || levels[BloomAnalizar] != 1 || levels[BloomEvaluar] != 1 || levels[BloomComprender] != 1 {
			t.Fatalf("seed %d: mezcla de Bloom %v", seed, levels)
		}
	}
}

func TestRotacionVuelveAlosMasAntiguosCuandoSeAgotaElBanco(t *testing.T) {
	pool := bankPool()
	rnd := rand.New(rand.NewSource(7))
	history := map[string]ItemUse{}
	for round := 0; round < 12; round++ {
		var list []ItemUse
		for _, u := range history {
			list = append(list, u)
		}
		items := DrawEvaluation(pool, recommendedM01, list, rnd)
		if len(items) != 5 {
			t.Fatalf("ronda %d: %d ítems", round, len(items))
		}
		seen := map[string]bool{}
		for _, it := range items {
			if seen[it.ItemCode] {
				t.Fatalf("ronda %d repite %s", round, it.ItemCode)
			}
			seen[it.ItemCode] = true
			u := history[it.ItemCode]
			u.ItemCode, u.Uses, u.LastUsed = it.ItemCode, u.Uses+1, time.Unix(int64(round), 0)
			history[it.ItemCode] = u
		}
	}
	// Tras 12 evaluaciones ningún ítem balanceado se usó más de lo razonable respecto a los demás de su nivel.
	counts := map[string]int{}
	for _, q := range pool {
		counts[q.Bloom] += 0
	}
	for _, q := range pool {
		if q.Bloom == BloomAplicar {
			max, min := 0, 1<<30
			for _, other := range pool {
				if other.Bloom == BloomAplicar {
					u := history[other.ItemCode].Uses
					if u > max {
						max = u
					}
					if u < min {
						min = u
					}
				}
			}
			if max-min > 1 {
				t.Fatalf("rotación desigual en Aplicar: máx %d mín %d", max, min)
			}
		}
	}
}

func TestSorteoConBancoPequenoUsaTodo(t *testing.T) {
	items := DrawEvaluation(bankPool()[:3], recommendedM01, nil, rand.New(rand.NewSource(3)))
	if len(items) != 3 {
		t.Fatalf("ítems = %d", len(items))
	}
}

func TestOpcionesAleatorizadasTraduceBienLaCalificacion(t *testing.T) {
	pool := bankPool()
	rnd := rand.New(rand.NewSource(42))
	draw := Draw{ID: "d", VersionID: "v", AttemptType: AttemptFinal, BankVersion: 1, Items: DrawEvaluation(pool, recommendedM01, nil, rnd)}
	byID := map[string]Question{}
	for _, q := range pool {
		byID[q.ID] = q
	}
	// Responde siempre la alternativa correcta tal como se mostró: debe puntuar 5/5 sin importar el orden.
	var answers []Answer
	positions := map[int]int{}
	for _, item := range draw.Items {
		q := byID[item.QuestionID]
		for shown, original := range item.OptionOrder {
			if original == q.CorrectOption {
				answers = append(answers, Answer{QuestionID: item.QuestionID, SelectedOption: shown})
				positions[shown]++
			}
		}
	}
	grade, err := GradeDraw(draw, pool, answers, 80)
	if err != nil {
		t.Fatal(err)
	}
	if grade.CorrectCount != 5 || grade.Score != 100 || !grade.Passed {
		t.Fatalf("calificación %+v", grade)
	}
	for _, r := range grade.Results {
		if !r.Correct || r.SelectedOption != r.CorrectOption || r.ItemCode == "" || len(r.Options) != 5 || len(r.Sources) == 0 {
			t.Fatalf("resultado %+v", r)
		}
		if got := r.Options[r.CorrectOption]; got != byID[r.QuestionID].Options[byID[r.QuestionID].CorrectOption] {
			t.Fatalf("la alternativa correcta mostrada %q no es la correcta original", got)
		}
		if r.StoredSelection() != byID[r.QuestionID].CorrectOption {
			t.Fatalf("se guardaría %d y la correcta original es %d", r.StoredSelection(), byID[r.QuestionID].CorrectOption)
		}
	}
	// 3 de 5 no aprueba; 4 de 5 sí (80 %).
	wrong := func(n int) Grade {
		out := append([]Answer(nil), answers...)
		for i := 0; i < n; i++ {
			out[i].SelectedOption = (out[i].SelectedOption + 1) % 5
		}
		g, err := GradeDraw(draw, pool, out, 80)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	if g := wrong(1); g.CorrectCount != 4 || !g.Passed || g.Score != 80 {
		t.Fatalf("4/5 = %+v", g)
	}
	if g := wrong(2); g.CorrectCount != 3 || g.Passed {
		t.Fatalf("3/5 = %+v", g)
	}
}

func TestCalificarSorteoRechazaRespuestasInvalidas(t *testing.T) {
	pool := bankPool()
	draw := Draw{Items: DrawEvaluation(pool, recommendedM01, nil, rand.New(rand.NewSource(5)))}
	ok := make([]Answer, 0, 5)
	for _, it := range draw.Items {
		ok = append(ok, Answer{QuestionID: it.QuestionID, SelectedOption: 0})
	}
	cases := map[string][]Answer{
		"faltante": ok[:4],
		"repetida": append(append([]Answer(nil), ok[:4]...), ok[0]),
		"ajena":    append(append([]Answer(nil), ok[:4]...), Answer{QuestionID: pool[19].ID, SelectedOption: 0}),
		"fuera":    append(append([]Answer(nil), ok[:4]...), Answer{QuestionID: ok[4].QuestionID, SelectedOption: 5}),
		"negativa": append(append([]Answer(nil), ok[:4]...), Answer{QuestionID: ok[4].QuestionID, SelectedOption: -1}),
	}
	for name, answers := range cases {
		if _, err := GradeDraw(draw, pool, answers, 80); err == nil {
			t.Errorf("%s: se esperaba un error", name)
		}
	}
}

func TestDisplayResultSinBarajarConservaLosIndices(t *testing.T) {
	q := bankPool()[0]
	r := DisplayResult(q, 2, nil)
	if r.SelectedOption != 2 || r.CorrectOption != q.CorrectOption || r.StoredSelection() != 2 {
		t.Fatalf("resultado %+v", r)
	}
}

func TestRepasoEspaciado(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	s, xp := NextPractice(PracticeState{}, false, now)
	if xp != 0 || s.Box != 0 || s.DueAt == nil || !s.DueAt.Equal(now) || s.LastCorrect {
		t.Fatalf("fallo: %+v xp %d", s, xp)
	}
	s, xp = NextPractice(s, true, now)
	if xp != PracticeXP || s.Box != 1 || !s.DueAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("primer acierto: %+v xp %d", s, xp)
	}
	s, xp = NextPractice(s, true, now)
	if xp != 0 || s.Box != 2 || !s.DueAt.Equal(now.Add(72*time.Hour)) {
		t.Fatalf("segundo acierto no repite XP: %+v xp %d", s, xp)
	}
	s, _ = NextPractice(s, true, now)
	s, _ = NextPractice(s, true, now)
	if s.Box != 4 || s.DueAt != nil {
		t.Fatalf("dominado: %+v", s)
	}
	s, _ = NextPractice(s, false, now)
	if s.Box != 0 || s.DueAt == nil || s.XP != PracticeXP {
		t.Fatalf("fallar tras dominar vuelve a repaso y conserva el XP: %+v", s)
	}
}
