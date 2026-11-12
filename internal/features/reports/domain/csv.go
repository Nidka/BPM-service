package domain

import (
	"strconv"
	"strings"
	"time"
)

// ExportHeader son las columnas del CSV del historial de checklists. No incluye personas.
var ExportHeader = []string{"runId", "localDate", "submittedAtUtc", "locationId", "locationName", "templateName", "area", "templateVersion", "compliancePercent"}

// ExportRow es una ejecución finalizada del historial exportable.
type ExportRow struct {
	RunID, LocationID, LocationName, TemplateName, Area string
	LocalDate                                           time.Time
	SubmittedAt                                         time.Time
	TemplateVersion                                     int
	CompliancePercent                                   float64
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
		r.RunID,
		r.LocalDate.Format(DateLayout),
		r.SubmittedAt.UTC().Format(time.RFC3339),
		r.LocationID,
		CSVText(r.LocationName),
		CSVText(r.TemplateName),
		CSVText(r.Area),
		strconv.Itoa(r.TemplateVersion),
		strconv.FormatFloat(r.CompliancePercent, 'f', 2, 64),
	}
}
