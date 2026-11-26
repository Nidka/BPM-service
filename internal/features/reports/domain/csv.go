package domain

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Formatos de presentación de la exportación (hora local de la sede, 24 h).
const (
	ExportDateLayout     = "02/01/2006"
	ExportDateTimeLayout = "02/01/2006 15:04"
	// fallbackZone se usa si la zona de la sede no se puede cargar (coherente con bpm_report_timezone).
	fallbackZone = "America/Lima"
)

// ExportHeader son las columnas del CSV del historial de checklists, en español y sin identificadores internos
// (fecha local, envío, sede, control, área, versión, cumplimiento). No incluye personas.
var ExportHeader = []string{"Fecha local", "Enviado el", "Sede", "Control", "Área", "Versión de la plantilla", "Cumplimiento (%)"}

// ExportRow es una ejecución finalizada del historial exportable.
type ExportRow struct {
	RunID, LocationID, LocationName, TemplateName, Area string
	LocalDate                                           time.Time
	SubmittedAt                                         time.Time
	// Zone es la zona IANA de la sede; con ella se muestran las horas locales.
	Zone              string
	TemplateVersion   int
	CompliancePercent float64
}

// Location devuelve la zona horaria de la sede (America/Lima si no se puede cargar).
func (r ExportRow) Location() *time.Location {
	if loc, err := time.LoadLocation(r.Zone); err == nil && r.Zone != "" {
		return loc
	}
	if loc, err := time.LoadLocation(fallbackZone); err == nil {
		return loc
	}
	return time.UTC
}

// SubmittedLocal formatea el envío como dd/MM/aaaa HH:mm en la hora local de la sede.
func (r ExportRow) SubmittedLocal() string {
	return r.SubmittedAt.In(r.Location()).Format(ExportDateTimeLayout)
}

// CSVText neutraliza la inyección de fórmulas en hojas de cálculo: si la celda empieza por
// =, +, -, @, tabulador o retorno de carro se antepone una comilla simple.
func CSVText(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// Record serializa la fila; los textos se escapan y los números son seguros por construcción.
func (r ExportRow) Record() []string {
	return []string{
		r.LocalDate.Format(ExportDateLayout),
		r.SubmittedLocal(),
		CSVText(r.LocationName),
		CSVText(r.TemplateName),
		CSVText(r.Area),
		strconv.Itoa(r.TemplateVersion),
		strconv.FormatFloat(r.CompliancePercent, 'f', 2, 64),
	}
}

// ExportDocument es el contenido del reporte PDF: datos del encabezado y todas las filas del período.
type ExportDocument struct {
	OrganizationName string
	Period           Period
	// LocationName vacío significa «todas las sedes» del alcance del usuario.
	LocationName string
	GeneratedAt  time.Time
	Rows         []ExportRow
}

// SiteStat es el cumplimiento promedio de una sede en el documento.
type SiteStat struct {
	Name    string
	Runs    int
	Average float64
}

// ExportStats resume el documento: total, promedio general (nil sin filas) y desglose por sede.
type ExportStats struct {
	Runs    int
	Average *float64
	Sites   []SiteStat
}

// Stats calcula el resumen del documento; las sedes salen ordenadas por nombre.
func (d ExportDocument) Stats() ExportStats {
	out := ExportStats{Runs: len(d.Rows)}
	if len(d.Rows) == 0 {
		return out
	}
	type acc struct {
		runs int
		sum  float64
	}
	bySite := map[string]*acc{}
	total := 0.0
	for _, r := range d.Rows {
		a := bySite[r.LocationName]
		if a == nil {
			a = &acc{}
			bySite[r.LocationName] = a
		}
		a.runs++
		a.sum += r.CompliancePercent
		total += r.CompliancePercent
	}
	avg := Round2(total / float64(len(d.Rows)))
	out.Average = &avg
	for name, a := range bySite {
		out.Sites = append(out.Sites, SiteStat{Name: name, Runs: a.runs, Average: Round2(a.sum / float64(a.runs))})
	}
	sort.Slice(out.Sites, func(i, j int) bool { return out.Sites[i].Name < out.Sites[j].Name })
	return out
}
