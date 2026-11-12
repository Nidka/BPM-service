package domain

import (
	"sort"
	"time"
)

// DailyRow es el agregado por sede y día local leído de checklist_runs SUBMITTED.
type DailyRow struct {
	LocationID, LocationName string
	Day                      time.Time
	Runs                     int
	Sum                      float64
}

// DayStat es el cumplimiento de un día local. Solo existen días con ejecuciones.
type DayStat struct {
	Date    string
	Runs    int
	Average *float64
}

// LocationCompliance es el cumplimiento de una sede en el período.
type LocationCompliance struct {
	LocationID, LocationName string
	Runs                     int
	Average                  *float64
	Days                     []DayStat
}

// ComplianceReport es el RF-16: cumplimiento por día, sede y empresa.
type ComplianceReport struct {
	From, To  string
	Runs      int
	Average   *float64
	Days      []DayStat
	Locations []LocationCompliance
}

// acc acumula ejecuciones y suma de puntajes.
type acc struct {
	runs int
	sum  float64
}

func (a *acc) add(runs int, sum float64) {
	a.runs += runs
	a.sum += sum
}

func accOf(m map[string]*acc, key string) *acc {
	if m[key] == nil {
		m[key] = &acc{}
	}
	return m[key]
}

func daysOf(m map[string]*acc) []DayStat {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]DayStat, 0, len(keys))
	for _, k := range keys {
		out = append(out, DayStat{Date: k, Runs: m[k].runs, Average: average(m[k].sum, m[k].runs)})
	}
	return out
}

// BuildCompliance agrega filas por sede/día. El promedio pondera cada ejecución por igual.
func BuildCompliance(p Period, rows []DailyRow) ComplianceReport {
	out := ComplianceReport{From: p.From.Format(DateLayout), To: p.To.Format(DateLayout), Days: []DayStat{}, Locations: []LocationCompliance{}}
	company := map[string]*acc{}
	total := &acc{}
	names := map[string]string{}
	totals := map[string]*acc{}
	days := map[string]map[string]*acc{}
	for _, r := range rows {
		day := r.Day.Format(DateLayout)
		accOf(company, day).add(r.Runs, r.Sum)
		total.add(r.Runs, r.Sum)
		names[r.LocationID] = r.LocationName
		accOf(totals, r.LocationID).add(r.Runs, r.Sum)
		if days[r.LocationID] == nil {
			days[r.LocationID] = map[string]*acc{}
		}
		accOf(days[r.LocationID], day).add(r.Runs, r.Sum)
	}
	out.Runs = total.runs
	out.Average = average(total.sum, total.runs)
	out.Days = daysOf(company)
	for id, name := range names {
		out.Locations = append(out.Locations, LocationCompliance{LocationID: id, LocationName: name,
			Runs: totals[id].runs, Average: average(totals[id].sum, totals[id].runs), Days: daysOf(days[id])})
	}
	sort.Slice(out.Locations, func(i, j int) bool {
		if out.Locations[i].LocationName != out.Locations[j].LocationName {
			return out.Locations[i].LocationName < out.Locations[j].LocationName
		}
		return out.Locations[i].LocationID < out.Locations[j].LocationID
	})
	return out
}
