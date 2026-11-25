// Package catalog contiene el contenido base de la Academia BPM que viaja con la
// aplicación: el banco oficial de 100 preguntas (5 módulos x 20 ítems, versionado) y la
// ruta de aprendizaje de cada módulo en unidades temáticas. Es solo datos y validación:
// no accede a la base de datos. La instalación en una organización la hace la capa de aplicación.
package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed banco_bpm_v1.json
var bancoJSON []byte

//go:embed cursos_bpm_v1.json
var cursosJSON []byte

// Niveles de la taxonomía de Bloom revisada que usa el banco.
const (
	BloomRecordar   = "Recordar"
	BloomComprender = "Comprender"
	BloomAplicar    = "Aplicar"
	BloomAnalizar   = "Analizar"
	BloomEvaluar    = "Evaluar"
)

// OptionLetters son las letras de las cinco alternativas del banco.
const OptionLetters = "ABCDE"

// Item es una pregunta del banco oficial. Key es la letra de la alternativa correcta.
type Item struct {
	Code          string            `json:"code"`
	Module        string            `json:"module"`
	Bloom         string            `json:"bloom"`
	Text          string            `json:"text"`
	Options       map[string]string `json:"options"`
	Key           string            `json:"key"`
	Justification string            `json:"justification"`
	Sources       []string          `json:"sources"`
}

// OptionTexts devuelve las alternativas en el orden A..E.
func (i Item) OptionTexts() []string {
	out := make([]string, 0, len(OptionLetters))
	for _, letter := range OptionLetters {
		out = append(out, i.Options[string(letter)])
	}
	return out
}

// KeyIndex es el índice 0..4 de la alternativa correcta.
func (i Item) KeyIndex() int { return strings.Index(OptionLetters, i.Key) }

// Source es una fuente oficial del banco (sección 7 del documento).
type Source struct {
	Code  string `json:"code"`
	Short string `json:"short"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Module es la cabecera de un módulo del banco.
type Module struct {
	Code       string `json:"code"`
	CourseCode string `json:"courseCode"`
	Title      string `json:"title"`
	Topics     string `json:"topics"`
}

// Bank es el banco completo y versionado.
type Bank struct {
	Version              int                 `json:"version"`
	Name                 string              `json:"name"`
	Status               string              `json:"status"`
	PassScorePercent     int                 `json:"passScorePercent"`
	ItemsPerEvaluation   int                 `json:"itemsPerEvaluation"`
	Modules              []Module            `json:"modules"`
	RecommendedSelection map[string][]string `json:"recommendedSelection"`
	Sources              []Source            `json:"sources"`
	Items                []Item              `json:"items"`

	byCode   map[string]Item
	bySource map[string]Source
}

// Item busca un ítem por su código (por ejemplo M01-03).
func (b *Bank) Item(code string) (Item, bool) { i, ok := b.byCode[code]; return i, ok }

// Source busca una fuente por su código (por ejemplo P01).
func (b *Bank) Source(code string) (Source, bool) { s, ok := b.bySource[code]; return s, ok }

// ModuleItems lista los ítems de un módulo en orden de código.
func (b *Bank) ModuleItems(module string) []Item {
	out := []Item{}
	for _, i := range b.Items {
		if i.Module == module {
			out = append(out, i)
		}
	}
	return out
}

// ModuleOf devuelve el módulo de un código de ítem (M01-03 -> M01) o "" si no tiene forma válida.
func ModuleOf(itemCode string) string {
	module, _, ok := strings.Cut(itemCode, "-")
	if !ok || len(module) != 3 {
		return ""
	}
	return module
}

// Recommended devuelve los ítems de la evaluación recomendada del módulo de un ítem.
func (b *Bank) Recommended(module string) []string {
	return append([]string(nil), b.RecommendedSelection[module]...)
}

// Unit es una unidad temática de un módulo: una lección corta y sus ítems de práctica.
type Unit struct {
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	KeyPoints []string `json:"keyPoints"`
	Items     []string `json:"items"`
}

// CourseModule es la ruta de aprendizaje de un módulo.
type CourseModule struct {
	Code        string `json:"code"`
	CourseCode  string `json:"courseCode"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Video es el video del módulo, compartido por todas las organizaciones (se muestra en la primera unidad).
	Video Video  `json:"video"`
	Units []Unit `json:"units"`
}

// Video describe el objeto de R2 de un video de la plataforma: su clave, tamaño y SHA-256 son los de la copia aprobada.
type Video struct {
	Code         string `json:"code"`
	ObjectKey    string `json:"objectKey"`
	OriginalName string `json:"originalName"`
	MIMEType     string `json:"mimeType"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
}

// Courses es la ruta de aprendizaje de los cinco módulos.
type Courses struct {
	Version          int            `json:"version"`
	Name             string         `json:"name"`
	EstimatedMinutes int            `json:"estimatedMinutes"`
	Modules          []CourseModule `json:"modules"`
}

// Module busca un módulo por su código (M01).
func (c *Courses) Module(code string) (CourseModule, bool) {
	for _, m := range c.Modules {
		if m.Code == code {
			return m, true
		}
	}
	return CourseModule{}, false
}

var (
	once       sync.Once
	loadedBank *Bank
	loadedCour *Courses
	loadErr    error
)

// Load decodifica y valida los datos embebidos una sola vez.
func Load() (*Bank, *Courses, error) {
	once.Do(func() {
		var bank Bank
		if err := json.Unmarshal(bancoJSON, &bank); err != nil {
			loadErr = fmt.Errorf("banco: %w", err)
			return
		}
		var courses Courses
		if err := json.Unmarshal(cursosJSON, &courses); err != nil {
			loadErr = fmt.Errorf("cursos: %w", err)
			return
		}
		if loadErr = bank.index(); loadErr != nil {
			return
		}
		if loadErr = courses.validate(&bank); loadErr != nil {
			return
		}
		loadedBank, loadedCour = &bank, &courses
	})
	return loadedBank, loadedCour, loadErr
}

// MustLoad es Load para el arranque: los datos embebidos son parte del binario.
func MustLoad() (*Bank, *Courses) {
	bank, courses, err := Load()
	if err != nil {
		panic("catálogo BPM inválido: " + err.Error())
	}
	return bank, courses
}

var validBloom = map[string]bool{BloomRecordar: true, BloomComprender: true, BloomAplicar: true, BloomAnalizar: true, BloomEvaluar: true}

func (b *Bank) index() error {
	b.byCode = make(map[string]Item, len(b.Items))
	b.bySource = make(map[string]Source, len(b.Sources))
	for _, s := range b.Sources {
		if s.Code == "" || s.Title == "" || s.Short == "" {
			return fmt.Errorf("fuente incompleta %q", s.Code)
		}
		b.bySource[s.Code] = s
	}
	for _, i := range b.Items {
		if _, dup := b.byCode[i.Code]; dup {
			return fmt.Errorf("ítem repetido %s", i.Code)
		}
		if ModuleOf(i.Code) != i.Module {
			return fmt.Errorf("ítem %s no pertenece al módulo %s", i.Code, i.Module)
		}
		if !validBloom[i.Bloom] {
			return fmt.Errorf("ítem %s con nivel de Bloom inválido %q", i.Code, i.Bloom)
		}
		if len(i.Options) != len(OptionLetters) || i.KeyIndex() < 0 || i.Options[i.Key] == "" {
			return fmt.Errorf("ítem %s con alternativas o clave inválidas", i.Code)
		}
		if strings.TrimSuffix(strings.TrimSpace(i.Justification), ".") != i.Options[i.Key] {
			return fmt.Errorf("la justificación de %s no coincide con la alternativa clave", i.Code)
		}
		for _, src := range i.Sources {
			if _, ok := b.bySource[src]; !ok {
				return fmt.Errorf("ítem %s cita la fuente desconocida %s", i.Code, src)
			}
		}
		b.byCode[i.Code] = i
	}
	for module, codes := range b.RecommendedSelection {
		if len(codes) != b.ItemsPerEvaluation {
			return fmt.Errorf("la selección recomendada de %s no tiene %d ítems", module, b.ItemsPerEvaluation)
		}
		for _, code := range codes {
			if i, ok := b.byCode[code]; !ok || i.Module != module {
				return fmt.Errorf("la selección recomendada de %s incluye %s", module, code)
			}
		}
	}
	return nil
}

// validate comprueba que cada ítem del banco esté en exactamente una unidad de su módulo y que
// el contenido respete los límites de la Academia (lección, «Lo esencial»).
func (c *Courses) validate(bank *Bank) error {
	seen := map[string]bool{}
	for _, m := range c.Modules {
		v := m.Video
		if v.Code == "" || !strings.HasPrefix(v.ObjectKey, "organizations/plataforma/training/videos/") || v.MIMEType != "video/mp4" || v.SizeBytes < 1 || len(v.SHA256) != 64 {
			return fmt.Errorf("el módulo %s necesita un video de plataforma válido", m.Code)
		}
		if len(m.Units) < 3 || len(m.Units) > 4 {
			return fmt.Errorf("el módulo %s debe tener 3 o 4 unidades", m.Code)
		}
		for _, u := range m.Units {
			if strings.TrimSpace(u.Title) == "" || strings.TrimSpace(u.Content) == "" || len(u.KeyPoints) == 0 || len(u.KeyPoints) > 5 {
				return fmt.Errorf("unidad incompleta en %s: %q", m.Code, u.Title)
			}
			points := map[string]bool{}
			for _, p := range u.KeyPoints {
				key := strings.ToLower(p)
				if p == "" || len([]rune(p)) > 200 || points[key] {
					return fmt.Errorf("idea clave inválida en %s: %q", m.Code, p)
				}
				points[key] = true
			}
			for _, code := range u.Items {
				item, ok := bank.byCode[code]
				if !ok || item.Module != m.Code {
					return fmt.Errorf("la unidad %q de %s incluye %s", u.Title, m.Code, code)
				}
				if seen[code] {
					return fmt.Errorf("el ítem %s está en más de una unidad", code)
				}
				seen[code] = true
			}
		}
	}
	if len(seen) != len(bank.Items) {
		missing := []string{}
		for _, i := range bank.Items {
			if !seen[i.Code] {
				missing = append(missing, i.Code)
			}
		}
		sort.Strings(missing)
		return errors.New("ítems sin unidad: " + strings.Join(missing, ", "))
	}
	return nil
}
