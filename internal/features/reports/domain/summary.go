package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TemplateRow es el agregado por plantilla (checklist) en el período.
type TemplateRow struct {
	TemplateID, Name, Area string
	Runs                   int
	Sum                    float64
}

// IncidentRow cuenta incidencias por sede: reportadas y cerradas en el período, y abiertas hoy.
type IncidentRow struct {
	LocationID                string
	Reported, Closed, OpenNow int
}

// TemperatureRow cuenta mediciones de recepción y las que quedaron fuera del rango registrado.
type TemperatureRow struct {
	LocationID        string
	Measured, OutOfRg int
}

// AreaStat es el cumplimiento agregado por área de checklist.
type AreaStat struct {
	Area    string
	Runs    int
	Average *float64
}

// TemplateStat es el cumplimiento de una plantilla.
type TemplateStat struct {
	TemplateID, Name, Area string
	Runs                   int
	Average                *float64
}

// LocationSummary es la fila por sede del reporte de empresa.
type LocationSummary struct {
	LocationID, LocationName string
	Runs                     int
	Average                  *float64
	IncidentsReported        int
	IncidentsClosed          int
	IncidentsOpenNow         int
	TemperatureMeasurements  int
	TemperatureOutOfRange    int
}

// IncidentTotals resume las incidencias de la empresa.
type IncidentTotals struct{ Reported, Closed, OpenNow int }

// TemperatureTotals resume las temperaturas de recepción.
type TemperatureTotals struct{ Measurements, OutOfRange int }

// Summary es el reporte de cumplimiento por empresa y período (RF-28).
type Summary struct {
	From, To     string
	Runs         int
	Average      *float64
	Locations    []LocationSummary
	Areas        []AreaStat
	Templates    []TemplateStat
	Incidents    IncidentTotals
	Temperatures TemperatureTotals
}

// BuildSummary combina las agregaciones. Toda sede de locations aparece, incluso sin actividad.
func BuildSummary(p Period, comp []DailyRow, templates []TemplateRow, incidents []IncidentRow, temps []TemperatureRow, locations map[string]string) Summary {
	report := BuildCompliance(p, comp)
	out := Summary{From: report.From, To: report.To, Runs: report.Runs, Average: report.Average,
		Locations: []LocationSummary{}, Areas: []AreaStat{}, Templates: []TemplateStat{}}
	byID := map[string]*LocationSummary{}
	for id, name := range locations {
		byID[id] = &LocationSummary{LocationID: id, LocationName: name}
	}
	for _, l := range report.Locations {
		if byID[l.LocationID] == nil {
			byID[l.LocationID] = &LocationSummary{LocationID: l.LocationID, LocationName: l.LocationName}
		}
		byID[l.LocationID].Runs, byID[l.LocationID].Average = l.Runs, l.Average
	}
	for _, i := range incidents {
		if v := byID[i.LocationID]; v != nil {
			v.IncidentsReported, v.IncidentsClosed, v.IncidentsOpenNow = i.Reported, i.Closed, i.OpenNow
			out.Incidents.Reported += i.Reported
			out.Incidents.Closed += i.Closed
			out.Incidents.OpenNow += i.OpenNow
		}
	}
	for _, t := range temps {
		if v := byID[t.LocationID]; v != nil {
			v.TemperatureMeasurements, v.TemperatureOutOfRange = t.Measured, t.OutOfRg
			out.Temperatures.Measurements += t.Measured
			out.Temperatures.OutOfRange += t.OutOfRg
		}
	}
	for _, v := range byID {
		out.Locations = append(out.Locations, *v)
	}
	sort.Slice(out.Locations, func(i, j int) bool {
		if out.Locations[i].LocationName != out.Locations[j].LocationName {
			return out.Locations[i].LocationName < out.Locations[j].LocationName
		}
		return out.Locations[i].LocationID < out.Locations[j].LocationID
	})
	areas := map[string]*acc{}
	for _, t := range templates {
		out.Templates = append(out.Templates, TemplateStat{TemplateID: t.TemplateID, Name: t.Name, Area: t.Area, Runs: t.Runs, Average: average(t.Sum, t.Runs)})
		accOf(areas, t.Area).add(t.Runs, t.Sum)
	}
	for area, a := range areas {
		out.Areas = append(out.Areas, AreaStat{Area: area, Runs: a.runs, Average: average(a.sum, a.runs)})
	}
	sort.Slice(out.Areas, func(i, j int) bool { return out.Areas[i].Area < out.Areas[j].Area })
	sort.Slice(out.Templates, func(i, j int) bool {
		if out.Templates[i].Name != out.Templates[j].Name {
			return out.Templates[i].Name < out.Templates[j].Name
		}
		return out.Templates[i].TemplateID < out.Templates[j].TemplateID
	})
	return out
}

// Week es la semana calendario (lunes a domingo, fechas locales de Lima) que cubre un correo.
type Week struct{ Start, End time.Time }

// WeekToReport devuelve la última semana completa cuyo correo ya debe enviarse: desde el lunes
// a las 08:00 (Lima) se reporta la semana anterior; antes de esa hora, la previa a ella.
func WeekToReport(now time.Time) Week {
	local := now.In(Lima)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	weeks := 1
	if local.Before(time.Date(monday.Year(), monday.Month(), monday.Day(), 8, 0, 0, 0, Lima)) {
		weeks = 2
	}
	start := monday.AddDate(0, 0, -7*weeks)
	return Week{Start: start, End: start.AddDate(0, 0, 6)}
}

// Period convierte la semana en período de consulta.
func (w Week) Period() Period { return Period{From: w.Start, To: w.End} }

// RenderWeeklyEmail compone asunto y cuerpo de texto plano con datos agregados; no incluye
// nombres ni correos de personas.
func RenderWeeklyEmail(organization string, w Week, s Summary) (subject, body string) {
	subject = "Resumen semanal BPM " + w.Start.Format(DateLayout) + " al " + w.End.Format(DateLayout)
	var b strings.Builder
	b.WriteString("Resumen semanal de cumplimiento BPM\n")
	b.WriteString("Empresa: " + singleLine(organization) + "\n")
	b.WriteString("Semana: " + w.Start.Format(DateLayout) + " al " + w.End.Format(DateLayout) + "\n\n")
	fmt.Fprintf(&b, "Controles finalizados: %d\n", s.Runs)
	b.WriteString("Cumplimiento promedio: " + percent(s.Average) + "\n")
	fmt.Fprintf(&b, "Incidencias reportadas: %d, cerradas: %d, abiertas hoy: %d\n", s.Incidents.Reported, s.Incidents.Closed, s.Incidents.OpenNow)
	fmt.Fprintf(&b, "Temperaturas fuera de rango en recepción: %d de %d mediciones\n", s.Temperatures.OutOfRange, s.Temperatures.Measurements)
	if len(s.Locations) > 0 {
		b.WriteString("\nPor sede:\n")
		for _, l := range s.Locations {
			fmt.Fprintf(&b, "- %s: %s (%d controles)\n", singleLine(l.LocationName), percent(l.Average), l.Runs)
		}
	}
	b.WriteString("\nPuedes desactivar este correo en la configuración de reportes de la empresa.\n")
	return subject, b.String()
}

func singleLine(v string) string { return strings.Join(strings.Fields(v), " ") }

func percent(v *float64) string {
	if v == nil {
		return "sin datos"
	}
	return fmt.Sprintf("%.2f %%", *v)
}
