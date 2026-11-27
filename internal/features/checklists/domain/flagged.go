package domain

import "strings"

// Flagged cuenta, en un control enviado, las respuestas que no cumplen (o miden fuera de rango) y las observaciones escritas.
func Flagged(v Run) (failures, observations int) {
	items := map[string]Item{}
	for _, it := range v.Items {
		items[it.ID] = it
	}
	for _, a := range v.Answers {
		it := items[a.ItemID]
		if a.ComplianceResult == "NON_COMPLIANT" || (a.NumericValue != nil && it.MinimumValue != nil && it.MaximumValue != nil &&
			(*a.NumericValue < *it.MinimumValue || *a.NumericValue > *it.MaximumValue)) {
			failures++
		}
		if strings.TrimSpace(a.Observation) != "" {
			observations++
		}
	}
	return
}

// FlaggedText arma el título y el mensaje del aviso al jefe: «Control con «No cumple»: <plantilla>» y «<sede> · 2 no cumplen · 1 observación».
func FlaggedText(template, site string, failures, observations int) (string, string) {
	title := "Control con observaciones"
	if failures > 0 {
		title = "Control con «No cumple»"
	}
	if t := strings.TrimSpace(template); t != "" {
		title += ": " + t
	}
	parts := []string{}
	if s := strings.TrimSpace(site); s != "" {
		parts = append(parts, s)
	}
	if failures == 1 {
		parts = append(parts, "1 no cumple")
	} else if failures > 1 {
		parts = append(parts, itoa(failures)+" no cumplen")
	}
	if observations == 1 {
		parts = append(parts, "1 observación")
	} else if observations > 1 {
		parts = append(parts, itoa(observations)+" observaciones")
	}
	return title, strings.Join(parts, " · ")
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
