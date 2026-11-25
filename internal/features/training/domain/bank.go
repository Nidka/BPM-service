package domain

import (
	"sort"
	"strings"
	"time"
)

// Parámetros del banco oficial de preguntas BPM.
const (
	// ItemsPerEvaluation son los ítems de cada evaluación de módulo (1 punto cada uno; aprueba 4/5 = 80 %).
	ItemsPerEvaluation = 5
	// PracticeXP es el XP por ítem la primera vez que se acierta en la práctica (una sola vez por ítem).
	PracticeXP = 2
	// ReviewBatch es el máximo de preguntas de un repaso.
	ReviewBatch = 5
)

// Niveles de Bloom del banco.
const (
	BloomRecordar   = "Recordar"
	BloomComprender = "Comprender"
	BloomAplicar    = "Aplicar"
	BloomAnalizar   = "Analizar"
	BloomEvaluar    = "Evaluar"
)

// bloomTargets es la mezcla orientativa de una evaluación: 1 comprender, 2 aplicar, 1 analizar y 1 evaluar.
var bloomTargets = []string{BloomAplicar, BloomAplicar, BloomAnalizar, BloomEvaluar, BloomComprender}

// BankInfo describe el banco que usa un curso.
type BankInfo struct{ Version, ItemsPerAttempt, PoolSize int }

// ReviewInfo son las preguntas que toca repasar: DueCount es el total pendiente y QuestionIDs un lote.
type ReviewInfo struct {
	DueCount    int
	QuestionIDs []string
}

// Rand es la fuente de azar del sorteo; las pruebas inyectan una determinista.
type Rand interface{ Intn(n int) int }

// DrawItem es un ítem sorteado con el orden en que se muestran sus alternativas:
// OptionOrder[i] es el índice original de la alternativa que se muestra en la posición i.
type DrawItem struct {
	QuestionID, ItemCode string
	OptionOrder          []int
}

// Draw es el sorteo persistido de una evaluación; su ID es el del intento que lo califica.
type Draw struct {
	ID, UserID, VersionID, AttemptType string
	BankVersion, AttemptNumber         int
	Items                              []DrawItem
	CreatedAt                          time.Time
}

// ItemUse resume cuántas veces vio una persona un ítem en evaluaciones terminadas y cuándo fue la última.
type ItemUse struct {
	ItemCode string
	Uses     int
	LastUsed time.Time
}

// BankPool filtra los ítems del banco de una versión.
func BankPool(questions []Question) []Question {
	out := make([]Question, 0, len(questions))
	for _, q := range questions {
		if q.FromBank() {
			out = append(out, q)
		}
	}
	return out
}

// Permutation devuelve un orden aleatorio de 0..n-1 (Fisher-Yates).
func Permutation(n int, rnd Rand) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j := rnd.Intn(i + 1)
		order[i], order[j] = order[j], order[i]
	}
	return order
}

// DrawEvaluation sortea los ítems de una evaluación. La primera evaluación de una persona (sin historial)
// usa la selección recomendada del banco; las siguientes rotan: priorizan los ítems menos vistos y, entre
// ellos, los vistos hace más tiempo, repartidos por nivel de Bloom (1 comprender, 2 aplicar, 1 analizar y
// 1 evaluar cuando el banco lo permite). Cada ítem lleva un orden aleatorio de alternativas, y los ítems
// se presentan en orden aleatorio.
func DrawEvaluation(pool []Question, recommended []string, history []ItemUse, rnd Rand) []DrawItem {
	count := ItemsPerEvaluation
	if len(pool) < count {
		count = len(pool)
	}
	var chosen []Question
	if len(history) == 0 {
		chosen = recommendedItems(pool, recommended, count)
	}
	if chosen == nil {
		chosen = rotatedItems(pool, history, count, rnd)
	}
	order := Permutation(len(chosen), rnd)
	items := make([]DrawItem, 0, len(chosen))
	for _, i := range order {
		q := chosen[i]
		items = append(items, DrawItem{QuestionID: q.ID, ItemCode: q.ItemCode, OptionOrder: Permutation(len(q.Options), rnd)})
	}
	return items
}

// recommendedItems devuelve la selección recomendada solo si todos sus ítems están en el banco de la versión.
func recommendedItems(pool []Question, recommended []string, count int) []Question {
	if len(recommended) != count || count == 0 {
		return nil
	}
	byCode := make(map[string]Question, len(pool))
	for _, q := range pool {
		byCode[q.ItemCode] = q
	}
	out := make([]Question, 0, count)
	for _, code := range recommended {
		q, ok := byCode[code]
		if !ok {
			return nil
		}
		out = append(out, q)
	}
	return out
}

func rotatedItems(pool []Question, history []ItemUse, count int, rnd Rand) []Question {
	uses := make(map[string]ItemUse, len(history))
	for _, u := range history {
		uses[u.ItemCode] = u
	}
	type ranked struct {
		q   Question
		tie int
	}
	rank := make([]ranked, len(pool))
	for i, q := range pool {
		rank[i] = ranked{q: q, tie: rnd.Intn(1 << 30)}
	}
	sort.SliceStable(rank, func(a, b int) bool {
		ua, ub := uses[rank[a].q.ItemCode], uses[rank[b].q.ItemCode]
		if ua.Uses != ub.Uses {
			return ua.Uses < ub.Uses
		}
		if !ua.LastUsed.Equal(ub.LastUsed) {
			return ua.LastUsed.Before(ub.LastUsed)
		}
		return rank[a].tie < rank[b].tie
	})
	taken := make(map[string]bool, count)
	var chosen []Question
	take := func(match func(Question) bool) bool {
		for _, r := range rank {
			if !taken[r.q.ItemCode] && match(r.q) {
				taken[r.q.ItemCode] = true
				chosen = append(chosen, r.q)
				return true
			}
		}
		return false
	}
	for _, level := range bloomTargets {
		if len(chosen) == count {
			break
		}
		take(func(q Question) bool { return q.Bloom == level })
	}
	for len(chosen) < count && take(func(Question) bool { return true }) {
	}
	return chosen
}

// ShownOptions devuelve las alternativas de q en el orden mostrado.
func ShownOptions(q Question, order []int) []string {
	out := make([]string, len(order))
	for i, original := range order {
		out[i] = q.Options[original]
	}
	return out
}

// DisplayResult arma el resultado de una pregunta del banco con las posiciones tal como se mostraron.
// selectedOriginal es el índice original elegido; order, el orden mostrado (nil = sin barajar).
func DisplayResult(q Question, selectedOriginal int, order []int) Result {
	if len(order) == 0 {
		order = make([]int, len(q.Options))
		for i := range order {
			order[i] = i
		}
	}
	r := Result{QuestionID: q.ID, Correct: selectedOriginal == q.CorrectOption, Explanation: q.Explanation,
		ItemCode: q.ItemCode, Bloom: q.Bloom, BankVersion: q.BankVersion, Sources: q.Sources,
		QuestionText: q.Text, Options: ShownOptions(q, order), OptionOrder: order}
	for shown, original := range order {
		if original == selectedOriginal {
			r.SelectedOption = shown
		}
		if original == q.CorrectOption {
			r.CorrectOption = shown
		}
	}
	return r
}

// GradeDraw califica una evaluación sorteada: exige exactamente una respuesta por ítem del sorteo con una
// posición mostrada válida; las posiciones se traducen al índice original con el orden del sorteo.
func GradeDraw(draw Draw, questions []Question, answers []Answer, passingScore int) (Grade, error) {
	fields := map[string]string{}
	byID := make(map[string]Question, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	items := make(map[string]DrawItem, len(draw.Items))
	for _, item := range draw.Items {
		items[item.QuestionID] = item
	}
	chosen := make(map[string]int, len(answers))
	for i, a := range answers {
		id := strings.ToLower(a.QuestionID)
		item, known := items[id]
		switch {
		case !known:
			fields[indexed("answers", i, "questionId")] = "La pregunta no pertenece a esta evaluación."
		case func() bool { _, dup := chosen[id]; return dup }():
			fields[indexed("answers", i, "questionId")] = "La pregunta se respondió más de una vez."
		case a.SelectedOption < 0 || a.SelectedOption >= len(item.OptionOrder):
			fields[indexed("answers", i, "selectedOption")] = "La opción elegida está fuera de rango."
		default:
			chosen[id] = a.SelectedOption
		}
	}
	if missing := len(draw.Items) - len(chosen); missing > 0 && len(fields) == 0 {
		fields["answers"] = "Faltan preguntas por responder."
	}
	if len(fields) > 0 {
		return Grade{}, &AnswersError{Fields: fields}
	}
	grade := Grade{Total: len(draw.Items), Results: make([]Result, 0, len(draw.Items))}
	for _, item := range draw.Items {
		q := byID[item.QuestionID]
		result := DisplayResult(q, item.OptionOrder[chosen[item.QuestionID]], item.OptionOrder)
		if result.Correct {
			grade.CorrectCount++
		}
		grade.Results = append(grade.Results, result)
	}
	grade.Score = ScorePercent(grade.CorrectCount, grade.Total)
	grade.Passed = grade.Score >= passingScore
	return grade, nil
}

// PracticeState es el estado de repaso de un ítem para una persona.
type PracticeState struct {
	Answered, Correct, Box, XP int
	LastCorrect                bool
	DueAt                      *time.Time
}

// reviewIntervals son los días hasta volver a ver un ítem acertado, según su caja (1 a 3).
var reviewIntervals = map[int]time.Duration{1: 24 * time.Hour, 2: 3 * 24 * time.Hour, 3: 7 * 24 * time.Hour}

// NextPractice aplica una respuesta de práctica: fallar vuelve el ítem a repaso inmediato y acertar sube de
// caja (1, 3 y 7 días; la cuarta lo da por dominado). Devuelve el nuevo estado y el XP ganado ahora.
func NextPractice(prev PracticeState, correct bool, now time.Time) (PracticeState, int) {
	next := prev
	next.Answered++
	next.LastCorrect = correct
	gained := 0
	if !correct {
		next.Box = 0
		due := now.UTC()
		next.DueAt = &due
		return next, 0
	}
	next.Correct++
	if prev.XP == 0 {
		gained = PracticeXP
		next.XP = PracticeXP
	}
	if next.Box < 4 {
		next.Box++
	}
	if interval, ok := reviewIntervals[next.Box]; ok {
		due := now.UTC().Add(interval)
		next.DueAt = &due
	} else {
		next.DueAt = nil
	}
	return next, gained
}
