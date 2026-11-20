package domain

import (
	"sort"
	"time"
)

// Constantes de la racha de aprendizaje.
const (
	// StreakWindowDays es la ventana de actividad que se consulta; la racha nunca supera este valor.
	StreakWindowDays = 400
	// DefaultStreakTimezone es la zona cuando la persona no tiene una sede activa asignada.
	DefaultStreakTimezone = "America/Lima"
)

// Streak es la racha de aprendizaje de una persona. LastActivityDate es la fecha local
// (AAAA-MM-DD) del último día con actividad, vacía si no hay ninguna en la ventana.
type Streak struct {
	CurrentDays, LongestDays int
	LastActivityDate         string
	ActiveToday              bool
	Timezone                 string
}

// CurrentStreak calcula la racha a partir de los instantes de actividad (intentos y
// lecciones completadas). Un día es el día calendario local en loc; varios eventos del
// mismo día cuentan una vez y los posteriores a hoy se ignoran. La racha vigente son los
// días consecutivos que terminan hoy o ayer (si el último día es anterior a ayer vale 0);
// LongestDays es la mayor racha de la ventana consultada.
func CurrentStreak(activity []time.Time, now time.Time, loc *time.Location) Streak {
	if loc == nil {
		loc = time.UTC
	}
	today := dayNumber(now, loc)
	seen := make(map[int64]bool, len(activity))
	days := make([]int64, 0, len(activity))
	for _, at := range activity {
		d := dayNumber(at, loc)
		if d > today || seen[d] {
			continue
		}
		seen[d] = true
		days = append(days, d)
	}
	out := Streak{Timezone: loc.String()}
	if len(days) == 0 {
		return out
	}
	sort.Slice(days, func(i, j int) bool { return days[i] > days[j] })
	run := 0
	for i, d := range days {
		if i > 0 && days[i-1]-1 == d {
			run++
		} else {
			run = 1
		}
		out.LongestDays = max(out.LongestDays, run)
	}
	last := days[0]
	out.LastActivityDate = time.Unix(last*86400, 0).UTC().Format("2006-01-02")
	out.ActiveToday = last == today
	if last == today || last == today-1 {
		out.CurrentDays = 1
		for i := 1; i < len(days) && days[i-1]-1 == days[i]; i++ {
			out.CurrentDays++
		}
	}
	return out
}

// dayNumber devuelve el número de día calendario local (días desde 1970-01-01) del instante en loc.
func dayNumber(at time.Time, loc *time.Location) int64 {
	y, m, d := at.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}
