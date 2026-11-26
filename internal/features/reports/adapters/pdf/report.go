package pdf

import (
	"bytes"
	"fmt"
	"strconv"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
)

// Colores del documento.
var (
	ink    = RGB{0.11, 0.12, 0.14}
	muted  = RGB{0.42, 0.45, 0.49}
	accent = RGB{0.07, 0.39, 0.33}
	band   = RGB{0.94, 0.95, 0.95}
	rule   = RGB{0.82, 0.84, 0.85}
	white  = RGB{1, 1, 1}
)

// Medidas de la página A4 (puntos).
const (
	margin     = 40.0
	contentW   = A4Width - 2*margin
	footerY    = A4Height - 28
	bottomEdge = A4Height - 52
	rowH       = 16.0
	headH      = 20.0
	fontSize   = 8.5
	cellPad    = 5.0
	// maxSiteRows limita el desglose por sede para que el resumen quepa en la primera página.
	maxSiteRows = 12
)

type column struct {
	title string
	width float64
	right bool
}

// Columnas del detalle; las dos últimas se alinean a la derecha.
var detailColumns = []column{
	{"Enviado el", 86, false}, {"Sede", 100, false}, {"Control", 140, false}, {"Área", 62, false}, {"Versión", 42, true}, {"Cumplimiento (%)", 85.28, true},
}

var siteColumns = []column{{"Sede", 305.28, false}, {"Controles", 80, true}, {"Cumplimiento promedio (%)", 130, true}}

type builder struct {
	doc  *Document
	page *Page
	y    float64
}

// Render arma el PDF A4 con el resumen del período y la tabla de controles. Los textos son datos de negocio ya
// autorizados; no incluyen personas. El PDF no es una hoja de cálculo, por lo que no necesita el escape de fórmulas del CSV.
func Render(src domain.ExportDocument) ([]byte, error) {
	d := &Document{
		Title:    "Historial de controles " + src.Period.From.Format(domain.ExportDateLayout) + " al " + src.Period.To.Format(domain.ExportDateLayout),
		Subject:  "Historial de controles finalizados",
		Producer: "BPM",
		Created:  src.GeneratedAt,
	}
	b := &builder{doc: d}
	b.newPage()
	b.header(src)
	stats := src.Stats()
	b.summary(src, stats)
	if len(stats.Sites) > 1 {
		b.sites(stats.Sites)
	}
	b.detail(src.Rows)
	b.footers()
	var out bytes.Buffer
	if _, err := d.WriteTo(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (b *builder) newPage() {
	b.page = b.doc.AddPage()
	b.y = margin
}

// ensure abre una página nueva si no caben h puntos; devuelve true si lo hizo.
func (b *builder) ensure(h float64) bool {
	if b.y+h <= bottomEdge {
		return false
	}
	b.newPage()
	return true
}

func (b *builder) header(src domain.ExportDocument) {
	p := b.page
	p.Rect(margin, b.y, 36, 3, accent)
	b.y += 20
	p.Text(margin, b.y, Bold, 8.5, accent, "BPM · REPORTE DE CUMPLIMIENTO")
	b.y += 26
	p.Text(margin, b.y, Bold, 22, ink, "Historial de controles")
	if name := src.OrganizationName; name != "" {
		b.y += 18
		p.Text(margin, b.y, Regular, 11, muted, Fit(Regular, 11, contentW, name))
	}
	b.y += 24
}

func (b *builder) summary(src domain.ExportDocument, stats domain.ExportStats) {
	p := b.page
	site := "Todas las sedes"
	if src.LocationName != "" {
		site = src.LocationName
	}
	lima, err := time.LoadLocation("America/Lima")
	if err != nil {
		lima = time.UTC
	}
	for _, kv := range [][2]string{
		{"Período", src.Period.From.Format(domain.ExportDateLayout) + " al " + src.Period.To.Format(domain.ExportDateLayout)},
		{"Sede", site},
		{"Generado el", src.GeneratedAt.In(lima).Format(domain.ExportDateTimeLayout) + " (hora de Lima)"},
	} {
		p.Text(margin, b.y, Bold, 9.5, muted, kv[0])
		p.Text(margin+80, b.y, Regular, 9.5, ink, Fit(Regular, 9.5, contentW-80, kv[1]))
		b.y += 15
	}
	b.y += 8
	average := "—"
	if stats.Average != nil {
		average = strconv.FormatFloat(*stats.Average, 'f', 2, 64) + " %"
	}
	sites := len(stats.Sites)
	boxes := [][2]string{{"Controles finalizados", strconv.Itoa(stats.Runs)}, {"Cumplimiento promedio", average}, {"Sedes con controles", strconv.Itoa(sites)}}
	gap := 12.0
	w := (contentW - 2*gap) / 3
	for i, box := range boxes {
		x := margin + float64(i)*(w+gap)
		p.Rect(x, b.y, w, 52, band)
		p.Rect(x, b.y, 3, 52, accent)
		p.Text(x+14, b.y+19, Regular, 8.5, muted, box[0])
		p.Text(x+14, b.y+41, Bold, 18, ink, box[1])
	}
	b.y += 52 + 26
}

func (b *builder) sectionTitle(text string) {
	b.page.Text(margin, b.y, Bold, 12, ink, text)
	b.y += 12
}

// tableHeader dibuja la fila de encabezado de una tabla.
func (b *builder) tableHeader(cols []column) {
	b.page.Rect(margin, b.y, contentW, headH, accent)
	x := margin
	for _, c := range cols {
		b.cell(x, b.y+13.5, c, Bold, white, c.title)
		x += c.width
	}
	b.y += headH
}

// cell escribe un texto dentro de su columna, recortado y alineado.
func (b *builder) cell(x, baseline float64, c column, f Font, color RGB, text string) {
	text = Fit(f, fontSize, c.width-2*cellPad, text)
	tx := x + cellPad
	if c.right {
		tx = x + c.width - cellPad - TextWidth(f, fontSize, text)
	}
	b.page.Text(tx, baseline, f, fontSize, color, text)
}

func (b *builder) sites(sites []domain.SiteStat) {
	b.ensure(headH + 3*rowH + 24)
	b.sectionTitle("Cumplimiento por sede")
	b.y += 8
	b.tableHeader(siteColumns)
	shown := sites
	if len(shown) > maxSiteRows {
		shown = shown[:maxSiteRows]
	}
	for i, s := range shown {
		b.row(siteColumns, i, []string{s.Name, strconv.Itoa(s.Runs), strconv.FormatFloat(s.Average, 'f', 2, 64)}, nil)
	}
	if extra := len(sites) - len(shown); extra > 0 {
		b.page.Text(margin+cellPad, b.y+11, Regular, 8, muted, fmt.Sprintf("y %d sedes más (consulta el CSV para el detalle completo)", extra))
		b.y += rowH
	}
	b.y += 22
}

// row dibuja una fila con bandas alternas; onBreak se invoca cuando la tabla continúa en una página nueva.
func (b *builder) row(cols []column, index int, cells []string, onBreak func()) {
	if b.ensure(rowH) && onBreak != nil {
		onBreak()
	}
	if index%2 == 1 {
		b.page.Rect(margin, b.y, contentW, rowH, band)
	}
	x := margin
	for i, c := range cols {
		b.cell(x, b.y+11, c, Regular, ink, cells[i])
		x += c.width
	}
	b.y += rowH
}

func (b *builder) detail(rows []domain.ExportRow) {
	b.ensure(headH + 3*rowH + 24)
	b.sectionTitle("Detalle de controles")
	b.y += 8
	if len(rows) == 0 {
		b.page.Rect(margin, b.y, contentW, 44, band)
		b.page.Text(margin+14, b.y+26, Regular, 10, muted, "No hay controles finalizados en este período.")
		b.y += 44
		return
	}
	b.tableHeader(detailColumns)
	for i, r := range rows {
		b.row(detailColumns, i, []string{r.SubmittedLocal(), r.LocationName, r.TemplateName, r.Area, "v" + strconv.Itoa(r.TemplateVersion), strconv.FormatFloat(r.CompliancePercent, 'f', 2, 64)}, func() {
			b.tableHeader(detailColumns)
		})
	}
	b.page.Line(margin, b.y, margin+contentW, b.y, 0.5, rule)
}

// footers agrega a cada página el pie con la numeración «Página i de n».
func (b *builder) footers() {
	total := b.doc.Pages()
	for i, p := range b.doc.pages {
		p.Line(margin, footerY-12, margin+contentW, footerY-12, 0.5, rule)
		p.Text(margin, footerY, Regular, 8, muted, "BPM · Historial de controles")
		label := fmt.Sprintf("Página %d de %d", i+1, total)
		p.Text(margin+contentW-TextWidth(Regular, 8, label), footerY, Regular, 8, muted, label)
	}
}
