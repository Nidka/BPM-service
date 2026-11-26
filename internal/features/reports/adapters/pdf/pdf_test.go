package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
)

// streams descomprime los flujos de contenido del PDF (uno por página).
func streams(t *testing.T, pdf []byte) []string {
	t.Helper()
	var out []string
	re := regexp.MustCompile(`(?s)/Length (\d+) /Filter /FlateDecode >>\nstream\n`)
	for _, m := range re.FindAllSubmatchIndex(pdf, -1) {
		n, _ := strconv.Atoi(string(pdf[m[2]:m[3]]))
		zr, err := zlib.NewReader(bytes.NewReader(pdf[m[1] : m[1]+n]))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	return out
}

// checkStructure verifica cabecera, fin de archivo y que la tabla xref apunte a cada objeto.
func checkStructure(t *testing.T, pdf []byte) int {
	t.Helper()
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatalf("cabecera o cierre inválidos: %q", pdf[:12])
	}
	idx := bytes.LastIndex(pdf, []byte("startxref\n"))
	var xrefAt int
	if _, err := fmt.Sscanf(string(pdf[idx+len("startxref\n"):]), "%d", &xrefAt); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf[xrefAt:], []byte("xref\n0 ")) {
		t.Fatalf("startxref no apunta a la tabla: %q", pdf[xrefAt:xrefAt+10])
	}
	var count int
	fmt.Sscanf(string(pdf[xrefAt+len("xref\n0 "):]), "%d", &count)
	lines := strings.Split(string(pdf[xrefAt:]), "\n")
	for i := 1; i < count; i++ {
		var off int
		fmt.Sscanf(lines[2+i], "%d", &off)
		if !bytes.HasPrefix(pdf[off:], []byte(fmt.Sprintf("%d 0 obj\n", i))) {
			t.Fatalf("el objeto %d no está en el desplazamiento %d", i, off)
		}
	}
	return count - 1
}

func sample(n int) domain.ExportDocument {
	at := time.Date(2026, 11, 10, 20, 35, 0, 0, time.UTC)
	rows := make([]domain.ExportRow, n)
	for i := range rows {
		site := "Sede Centro"
		if i%2 == 1 {
			site = "Sede Miraflores"
		}
		rows[i] = domain.ExportRow{RunID: fmt.Sprint(i), LocationName: site, TemplateName: "Apertura de cocina (turno mañana)", Area: "Cocina", LocalDate: at, SubmittedAt: at.Add(time.Duration(i) * time.Minute), Zone: "America/Lima", TemplateVersion: 2, CompliancePercent: 90 + float64(i%10)}
	}
	return domain.ExportDocument{OrganizationName: "Restaurante Ñandú S.A.C.", Period: domain.Period{From: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)}, GeneratedAt: at, Rows: rows}
}

func TestRenderEstructuraYContenido(t *testing.T) {
	pdf, err := Render(sample(3))
	if err != nil {
		t.Fatal(err)
	}
	// 1 catálogo + 1 páginas + 2 fuentes + 1 info + (página + contenido) por página.
	if objects := checkStructure(t, pdf); objects != 7 {
		t.Fatalf("objetos: %d", objects)
	}
	text := strings.Join(streams(t, pdf), "\n")
	for _, want := range []string{"(Historial de controles)", "(Per\\355odo)", "(10/11/2026 15:35)", "(Sede Miraflores)", "(Cumplimiento por sede)", "(P\\341gina 1 de 1)", "(Todas las sedes)", "(3)"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %s en el contenido:\n%s", want, text)
		}
	}
	if !strings.Contains(string(pdf), "/MediaBox [0 0 595.28 841.89]") {
		t.Error("la página debe ser A4")
	}
}

func TestRenderPaginaYEncabezadoRepetido(t *testing.T) {
	pdf, err := Render(sample(120))
	if err != nil {
		t.Fatal(err)
	}
	pages := streams(t, pdf)
	if len(pages) < 3 {
		t.Fatalf("120 filas deben ocupar varias páginas: %d", len(pages))
	}
	checkStructure(t, pdf)
	for i, page := range pages {
		if i > 0 && !strings.Contains(page, "(Enviado el)") {
			t.Errorf("la página %d no repite el encabezado de la tabla", i+1)
		}
		if want := fmt.Sprintf("(P\\341gina %d de %d)", i+1, len(pages)); !strings.Contains(page, want) {
			t.Errorf("falta %s", want)
		}
	}
}

func TestRenderSinControles(t *testing.T) {
	doc := sample(0)
	doc.LocationName = "Sede Centro"
	pdf, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	checkStructure(t, pdf)
	text := strings.Join(streams(t, pdf), "\n")
	if !strings.Contains(text, "(No hay controles finalizados en este per\\355odo.)") || !strings.Contains(text, "(Sede Centro)") || strings.Contains(text, "(Cumplimiento por sede)") {
		t.Fatalf("contenido de un reporte vacío:\n%s", text)
	}
}

func TestLiteralEscapaYCodificaWinAnsi(t *testing.T) {
	cases := map[string]string{
		`a(b)c\d`:          `(a\(b\)c\\d)`,
		"Ñandú ¿qué?":      `(\321and\372 \277qu\351?)`,
		"salto\nlínea":     `(salto l\355nea)`,
		"viñeta…–":         `(vi\361eta\205\226)`,
		"japonés 日本":       `(japon\351s ??)`,
		"emoji \U0001F600": `(emoji ?)`,
	}
	for in, want := range cases {
		if got := literal(in); got != want {
			t.Errorf("literal(%q) = %s, quiero %s", in, got, want)
		}
	}
}

func TestFitRecortaPorAncho(t *testing.T) {
	long := strings.Repeat("Muy largo ", 20)
	got := Fit(Regular, 8.5, 100, long)
	if !strings.HasSuffix(got, "…") || TextWidth(Regular, 8.5, got) > 100 {
		t.Fatalf("recorte: %q (%.1f pt)", got, TextWidth(Regular, 8.5, got))
	}
	if Fit(Regular, 8.5, 100, "Corto") != "Corto" {
		t.Fatal("un texto corto no se modifica")
	}
	if TextWidth(Regular, 10, "ÁéñÜ") != TextWidth(Regular, 10, "AenU") {
		t.Fatal("el ancho de una letra acentuada debe igualar al de la base")
	}
}

func TestMetadatosEnUTF16(t *testing.T) {
	d := &Document{Title: "Ñ"}
	d.AddPage()
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "/Title <FEFF00D1>") {
		t.Fatalf("título: %s", buf.String())
	}
}

func TestWrapPartePorPalabrasYPalabrasLargas(t *testing.T) {
	lines := Wrap(Regular, 8.5, 80, "Control de apertura de cocina y almacén del turno mañana", 6)
	if len(lines) < 2 {
		t.Fatalf("debe partirse en varias líneas: %v", lines)
	}
	if strings.Join(lines, " ") != "Control de apertura de cocina y almacén del turno mañana" {
		t.Fatalf("no debe perder texto: %v", lines)
	}
	for _, l := range lines {
		if TextWidth(Regular, 8.5, l) > 80 {
			t.Fatalf("línea más ancha que la columna: %q", l)
		}
	}
	long := Wrap(Regular, 8.5, 60, strings.Repeat("W", 40), 10)
	if len(long) < 2 || strings.Join(long, "") != strings.Repeat("W", 40) {
		t.Fatalf("una palabra larga se parte por caracteres: %v", long)
	}
	capped := Wrap(Regular, 8.5, 60, strings.Repeat("palabra ", 50), 3)
	if len(capped) != 3 || !strings.HasSuffix(capped[2], "…") {
		t.Fatalf("límite de líneas: %v", capped)
	}
	if got := Wrap(Regular, 8.5, 60, "  ", 3); len(got) != 1 || got[0] != "" {
		t.Fatalf("vacío: %v", got)
	}
}

func TestRenderMuestraNombresLargosCompletos(t *testing.T) {
	doc := sample(2)
	doc.Rows[0].TemplateName = "Control integral de apertura de cocina, almacén seco y cámaras frigoríficas del turno mañana"
	doc.Rows[0].LocationName = "Sede Delivery Miraflores Avenida Larco"
	pdf, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	checkStructure(t, pdf)
	text := strings.Join(streams(t, pdf), "\n")
	if strings.Contains(text, "…") || strings.Contains(text, "\\205") {
		t.Fatalf("no debe truncar con puntos suspensivos:\n%s", text)
	}
	for _, word := range []string{"frigor\\355ficas", "Larco", "ma\\361ana"} {
		if !strings.Contains(text, word) {
			t.Errorf("falta %s", word)
		}
	}
}
