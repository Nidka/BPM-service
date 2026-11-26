// Package pdf genera el reporte PDF del historial de controles. Incluye un escritor PDF mínimo (sin dependencias
// externas): páginas con texto, rectángulos y líneas, fuentes estándar Helvetica y codificación WinAnsi, que cubre
// el español completo.
package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Font identifica una de las fuentes estándar incluidas.
type Font int

// Fuentes disponibles (Type1 estándar; los visores las incorporan sin necesidad de embeberlas).
const (
	Regular Font = iota
	Bold
)

func (f Font) resource() string {
	if f == Bold {
		return "F2"
	}
	return "F1"
}

// RGB es un color con componentes de 0 a 1.
type RGB struct{ R, G, B float64 }

// Page es una página con origen arriba a la izquierda: y crece hacia abajo, como en un diseño de pantalla.
type Page struct {
	W, H float64
	buf  bytes.Buffer
}

// Document acumula páginas y las serializa como un PDF 1.4.
type Document struct {
	Title, Subject, Producer string
	Created                  time.Time
	pages                    []*Page
}

// A4 en puntos (1 pt = 1/72 pulgada).
const (
	A4Width  = 595.28
	A4Height = 841.89
)

// AddPage agrega una página A4 vertical.
func (d *Document) AddPage() *Page {
	p := &Page{W: A4Width, H: A4Height}
	d.pages = append(d.pages, p)
	return p
}

// Pages devuelve la cantidad de páginas.
func (d *Document) Pages() int { return len(d.pages) }

func num(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// Text escribe s con la línea base en (x, y). El texto se codifica en WinAnsi; lo no representable se muestra como «?».
func (p *Page) Text(x, y float64, f Font, size float64, c RGB, s string) {
	fmt.Fprintf(&p.buf, "BT /%s %s Tf %s %s %s rg 1 0 0 1 %s %s Tm %s Tj ET\n", f.resource(), num(size), num(c.R), num(c.G), num(c.B), num(x), num(p.H-y), literal(s))
}

// Rect rellena un rectángulo cuya esquina superior izquierda es (x, y).
func (p *Page) Rect(x, y, w, h float64, c RGB) {
	fmt.Fprintf(&p.buf, "%s %s %s rg %s %s %s %s re f\n", num(c.R), num(c.G), num(c.B), num(x), num(p.H-y-h), num(w), num(h))
}

// Line traza una línea horizontal o vertical de grosor w.
func (p *Page) Line(x1, y1, x2, y2, w float64, c RGB) {
	fmt.Fprintf(&p.buf, "%s %s %s RG %s w %s %s m %s %s l S\n", num(c.R), num(c.G), num(c.B), num(w), num(x1), num(p.H-y1), num(x2), num(p.H-y2))
}

// TextWidth estima el ancho de s en puntos con las métricas de Helvetica.
func TextWidth(f Font, size float64, s string) float64 {
	total := 0
	for _, r := range s {
		total += glyphWidth(f, r)
	}
	return float64(total) * size / 1000
}

// Fit recorta s con «…» para que no supere maxWidth puntos.
func Fit(f Font, size, maxWidth float64, s string) string {
	if TextWidth(f, size, s) <= maxWidth {
		return s
	}
	ellipsis := glyphWidth(f, '…')
	limit := (maxWidth * 1000 / size) - float64(ellipsis)
	used := 0
	var out []rune
	for _, r := range s {
		w := glyphWidth(f, r)
		if float64(used+w) > limit {
			break
		}
		used += w
		out = append(out, r)
	}
	return strings.TrimRight(string(out), " ") + "…"
}

// winAnsi convierte un rune al byte de la codificación WinAnsi (Windows-1252); ok es falso si no existe.
func winAnsi(r rune) (byte, bool) {
	switch {
	case r >= 0x20 && r < 0x7F, r >= 0xA0 && r <= 0xFF:
		return byte(r), true
	}
	switch r {
	case '€':
		return 0x80, true
	case '…':
		return 0x85, true
	case '‘':
		return 0x91, true
	case '’':
		return 0x92, true
	case '“':
		return 0x93, true
	case '”':
		return 0x94, true
	case '•':
		return 0x95, true
	case '–':
		return 0x96, true
	case '—':
		return 0x97, true
	case '™':
		return 0x99, true
	}
	return 0, false
}

// literal produce una cadena literal PDF «(...)» con los bytes WinAnsi escapados.
func literal(s string) string {
	var b strings.Builder
	b.WriteByte('(')
	for _, r := range s {
		c, ok := winAnsi(r)
		switch {
		case !ok && (r == '\t' || r == '\n' || r == '\r'):
			c = ' '
		case !ok:
			c = '?'
		}
		switch {
		case c == '(' || c == ')' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c < 0x20 || c > 0x7E:
			fmt.Fprintf(&b, "\\%03o", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(')')
	return b.String()
}

// textString codifica un texto de metadatos como UTF-16BE con marca de orden, válido para cualquier carácter.
func textString(s string) string {
	units := utf16.Encode([]rune(s))
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, u := range units {
		fmt.Fprintf(&b, "%04X", u)
	}
	b.WriteByte('>')
	return b.String()
}

// WriteTo serializa el documento. Los flujos de contenido se comprimen con Flate.
func (d *Document) WriteTo(w io.Writer) (int64, error) {
	if len(d.pages) == 0 {
		d.AddPage()
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := []int{0} // el objeto 0 es el principio de la lista libre
	begin := func() int {
		offsets = append(offsets, out.Len())
		n := len(offsets) - 1
		fmt.Fprintf(&out, "%d 0 obj\n", n)
		return n
	}
	end := func() { out.WriteString("endobj\n") }

	kids := make([]string, len(d.pages))
	for i := range d.pages {
		kids[i] = fmt.Sprintf("%d 0 R", 6+2*i)
	}
	begin() // 1: catálogo
	out.WriteString("<< /Type /Catalog /Pages 2 0 R >>\n")
	end()
	begin() // 2: árbol de páginas
	fmt.Fprintf(&out, "<< /Type /Pages /Count %d /Kids [%s] >>\n", len(d.pages), strings.Join(kids, " "))
	end()
	for _, name := range []string{"Helvetica", "Helvetica-Bold"} { // 3 y 4: fuentes
		begin()
		fmt.Fprintf(&out, "<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding /WinAnsiEncoding >>\n", name)
		end()
	}
	begin() // 5: información del documento
	out.WriteString("<<")
	if d.Title != "" {
		out.WriteString(" /Title " + textString(d.Title))
	}
	if d.Subject != "" {
		out.WriteString(" /Subject " + textString(d.Subject))
	}
	if d.Producer != "" {
		out.WriteString(" /Producer " + textString(d.Producer) + " /Creator " + textString(d.Producer))
	}
	if !d.Created.IsZero() {
		out.WriteString(" /CreationDate (D:" + d.Created.UTC().Format("20060102150405") + "Z)")
	}
	out.WriteString(" >>\n")
	end()
	for i, p := range d.pages {
		begin() // página
		fmt.Fprintf(&out, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>\n", num(p.W), num(p.H), 7+2*i)
		end()
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		if _, err := zw.Write(p.buf.Bytes()); err != nil {
			return 0, err
		}
		if err := zw.Close(); err != nil {
			return 0, err
		}
		begin() // contenido
		fmt.Fprintf(&out, "<< /Length %d /Filter /FlateDecode >>\nstream\n", z.Len())
		out.Write(z.Bytes())
		out.WriteString("\nendstream\n")
		end()
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, o := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 5 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	n, err := w.Write(out.Bytes())
	return int64(n), err
}

// Wrap reparte s en líneas de a lo sumo maxWidth puntos, cortando en los espacios; una palabra más ancha que la
// columna se parte por caracteres. Si hay más de maxLines líneas, la última se recorta con «…».
func Wrap(f Font, size, maxWidth float64, s string, maxLines int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	flush := func() {
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
	}
	for _, w := range words {
		for TextWidth(f, size, w) > maxWidth { // palabra más ancha que la columna
			flush()
			cut := 1
			for cut < len([]rune(w)) && TextWidth(f, size, string([]rune(w)[:cut+1])) <= maxWidth {
				cut++
			}
			lines = append(lines, string([]rune(w)[:cut]))
			w = string([]rune(w)[cut:])
		}
		switch {
		case w == "":
		case cur == "":
			cur = w
		case TextWidth(f, size, cur+" "+w) <= maxWidth:
			cur += " " + w
		default:
			flush()
			cur = w
		}
	}
	flush()
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = Fit(f, size, maxWidth, lines[maxLines-1]+"…")
		if !strings.HasSuffix(lines[maxLines-1], "…") {
			lines[maxLines-1] += "…"
		}
	}
	return lines
}
