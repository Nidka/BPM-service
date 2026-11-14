package postgres

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
)

// scopeCTE define las sedes visibles. Parámetros: $1 organización, $2 acceso a todas las sedes,
// $3 usuario, $4 sede opcional. La zona horaria se resuelve una sola vez por sede.
const scopeCTE = `scope AS (
 SELECT l.id, l.name, l.status, bpm_report_timezone(l.timezone) AS tz FROM locations l
 WHERE l.organization_id=$1 AND ($4::uuid IS NULL OR l.id=$4::uuid)
 AND ($2::boolean OR EXISTS(SELECT 1 FROM membership_locations ml WHERE ml.organization_id=l.organization_id AND ml.user_id=$3 AND ml.location_id=l.id)))`

// runsFilter selecciona ejecuciones finalizadas del período en fecha local de la sede ($5 y $6).
// El prefiltro UTC permite usar el índice; la zona horaria exacta decide el día.
const runsFilter = ` r.organization_id=$1 AND r.status='SUBMITTED' AND r.compliance_percent IS NOT NULL AND r.submitted_at IS NOT NULL
 AND r.submitted_at >= ($5::date::timestamp - interval '1 day') AT TIME ZONE 'UTC'
 AND r.submitted_at < ($6::date::timestamp + interval '2 days') AT TIME ZONE 'UTC'
 AND (r.submitted_at AT TIME ZONE s.tz)::date BETWEEN $5::date AND $6::date`

func args(sc domain.Scope) []any {
	var location any
	if sc.LocationID != "" {
		location = sc.LocationID
	}
	return []any{sc.OrganizationID, sc.AllLocations, sc.UserID, location}
}

func periodArgs(sc domain.Scope) []any {
	return append(args(sc), sc.Period.From, sc.Period.To)
}

// LocationNames devuelve las sedes del alcance.
func (s *store) LocationNames(ctx context.Context, sc domain.Scope) (map[string]string, error) {
	rows, err := s.tx.Query(ctx, `WITH `+scopeCTE+` SELECT id::text, name FROM scope`, args(sc)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ComplianceRows agrega el puntaje calculado por el servidor por sede y día local.
func (s *store) ComplianceRows(ctx context.Context, sc domain.Scope) ([]domain.DailyRow, error) {
	rows, err := s.tx.Query(ctx, `WITH `+scopeCTE+`
 SELECT s.id::text, s.name, (r.submitted_at AT TIME ZONE s.tz)::date AS day, count(*), COALESCE(sum(r.compliance_percent),0)::float8
 FROM checklist_runs r JOIN scope s ON s.id=r.location_id
 WHERE`+runsFilter+`
 GROUP BY s.id, s.name, day ORDER BY day, s.name, s.id`, periodArgs(sc)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.DailyRow{}
	for rows.Next() {
		var v domain.DailyRow
		if err = rows.Scan(&v.LocationID, &v.LocationName, &v.Day, &v.Runs, &v.Sum); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// TemplateRows agrega el cumplimiento por plantilla.
func (s *store) TemplateRows(ctx context.Context, sc domain.Scope) ([]domain.TemplateRow, error) {
	rows, err := s.tx.Query(ctx, `WITH `+scopeCTE+`
 SELECT t.id::text, t.name, t.area, count(*), COALESCE(sum(r.compliance_percent),0)::float8
 FROM checklist_runs r JOIN scope s ON s.id=r.location_id
 JOIN checklist_template_versions v ON v.organization_id=r.organization_id AND v.id=r.template_version_id
 JOIN checklist_templates t ON t.organization_id=v.organization_id AND t.id=v.template_id
 WHERE`+runsFilter+`
 GROUP BY t.id, t.name, t.area ORDER BY t.name, t.id`, periodArgs(sc)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TemplateRow{}
	for rows.Next() {
		var v domain.TemplateRow
		if err = rows.Scan(&v.TemplateID, &v.Name, &v.Area, &v.Runs, &v.Sum); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// IncidentRows cuenta incidencias reportadas y cerradas en el período y las abiertas hoy.
func (s *store) IncidentRows(ctx context.Context, sc domain.Scope) ([]domain.IncidentRow, error) {
	rows, err := s.tx.Query(ctx, `WITH `+scopeCTE+`
 SELECT s.id::text,
  count(*) FILTER (WHERE (i.created_at AT TIME ZONE s.tz)::date BETWEEN $5::date AND $6::date),
  count(*) FILTER (WHERE i.status='CLOSED' AND i.closed_at IS NOT NULL AND (i.closed_at AT TIME ZONE s.tz)::date BETWEEN $5::date AND $6::date),
  count(*) FILTER (WHERE i.status<>'CLOSED' AND (i.created_at AT TIME ZONE s.tz)::date <= $6::date)
 FROM incidents i JOIN scope s ON s.id=i.location_id
 WHERE i.organization_id=$1 AND i.created_at < ($6::date::timestamp + interval '2 days') AT TIME ZONE 'UTC'
 GROUP BY s.id`, periodArgs(sc)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.IncidentRow{}
	for rows.Next() {
		var v domain.IncidentRow
		if err = rows.Scan(&v.LocationID, &v.Reported, &v.Closed, &v.OpenNow); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// TemperatureRows cuenta mediciones de recepción con criterio registrado y las fuera de rango.
func (s *store) TemperatureRows(ctx context.Context, sc domain.Scope) ([]domain.TemperatureRow, error) {
	rows, err := s.tx.Query(ctx, `WITH `+scopeCTE+`
 SELECT s.id::text, count(*), count(*) FILTER (WHERE i.temperature_celsius < i.minimum_temperature OR i.temperature_celsius > i.maximum_temperature)
 FROM supply_receipts r JOIN scope s ON s.id=r.location_id
 JOIN supply_receipt_items i ON i.organization_id=r.organization_id AND i.receipt_id=r.id
 WHERE r.organization_id=$1 AND i.temperature_celsius IS NOT NULL AND i.minimum_temperature IS NOT NULL AND i.maximum_temperature IS NOT NULL
 AND r.received_at >= ($5::date::timestamp - interval '1 day') AT TIME ZONE 'UTC'
 AND r.received_at < ($6::date::timestamp + interval '2 days') AT TIME ZONE 'UTC'
 AND (r.received_at AT TIME ZONE s.tz)::date BETWEEN $5::date AND $6::date
 GROUP BY s.id`, periodArgs(sc)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TemperatureRow{}
	for rows.Next() {
		var v domain.TemperatureRow
		if err = rows.Scan(&v.LocationID, &v.Measured, &v.OutOfRg); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// KPIs calcula cumplimiento hoy/7/30 días, controles pendientes, alertas e incidencias abiertas.
// "Hoy" es la fecha local de cada sede en el instante now.
func (s *store) KPIs(ctx context.Context, sc domain.Scope, now time.Time) (domain.KPIs, error) {
	var out domain.KPIs
	var runs [3]int
	var sums [3]float64
	today := `today AS (SELECT s.id, s.status, s.tz, ($5::timestamptz AT TIME ZONE s.tz)::date AS d FROM scope s)`
	local := `(r.submitted_at AT TIME ZONE t.tz)::date`
	err := s.tx.QueryRow(ctx, `WITH `+scopeCTE+`, `+today+`
 SELECT count(*) FILTER (WHERE `+local+`=t.d), COALESCE(sum(r.compliance_percent) FILTER (WHERE `+local+`=t.d),0)::float8,
  count(*) FILTER (WHERE `+local+`>t.d-7), COALESCE(sum(r.compliance_percent) FILTER (WHERE `+local+`>t.d-7),0)::float8,
  count(*) FILTER (WHERE `+local+`>t.d-30), COALESCE(sum(r.compliance_percent) FILTER (WHERE `+local+`>t.d-30),0)::float8
 FROM checklist_runs r JOIN today t ON t.id=r.location_id
 WHERE r.organization_id=$1 AND r.status='SUBMITTED' AND r.compliance_percent IS NOT NULL AND r.submitted_at IS NOT NULL
 AND r.submitted_at >= $5::timestamptz - interval '32 days' AND r.submitted_at <= $5::timestamptz`, append(args(sc), now)...).
		Scan(&runs[0], &sums[0], &runs[1], &sums[1], &runs[2], &sums[2])
	if err != nil {
		return out, err
	}
	out.Today = window(runs[0], sums[0])
	out.Last7Days = window(runs[1], sums[1])
	out.Last30Days = window(runs[2], sums[2])
	err = s.tx.QueryRow(ctx, `WITH `+scopeCTE+`, `+today+`
 SELECT count(*), count(*) FILTER (WHERE NOT x.done),
  count(*) FILTER (WHERE NOT x.done AND a.due_time IS NOT NULL AND a.due_time < ($5::timestamptz AT TIME ZONE t.tz)::time)
 FROM checklist_assignments a JOIN today t ON t.id=a.location_id AND t.status='ACTIVE'
 JOIN checklist_templates tp ON tp.organization_id=a.organization_id AND tp.id=a.template_id AND tp.status='ACTIVE'
 CROSS JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM checklist_runs r WHERE r.organization_id=a.organization_id AND r.assignment_id=a.id AND r.status='SUBMITTED' AND (r.submitted_at AT TIME ZONE t.tz)::date=t.d) AS done) x
 WHERE a.organization_id=$1 AND a.active AND a.frequency='DAILY'
 AND EXISTS(SELECT 1 FROM checklist_template_versions v WHERE v.organization_id=a.organization_id AND v.template_id=a.template_id AND v.status='PUBLISHED')`, append(args(sc), now)...).
		Scan(&out.Pending.Assigned, &out.Pending.Pending, &out.Pending.Overdue)
	if err != nil {
		return out, err
	}
	err = s.tx.QueryRow(ctx, `WITH `+scopeCTE+`
 SELECT count(*) FROM alerts a WHERE a.organization_id=$1 AND a.status IN ('PENDING','SENT','VIEWED')
 AND ($2::boolean OR a.location_id IN (SELECT id FROM scope))`, args(sc)...).Scan(&out.OpenAlerts)
	if err != nil {
		return out, err
	}
	err = s.tx.QueryRow(ctx, `WITH `+scopeCTE+`
 SELECT count(*) FROM incidents i JOIN scope s ON s.id=i.location_id WHERE i.organization_id=$1 AND i.status<>'CLOSED'`, args(sc)...).Scan(&out.OpenIncidents)
	return out, err
}

func window(runs int, sum float64) domain.WindowStat {
	w := domain.WindowStat{Runs: runs}
	if runs > 0 {
		v := domain.Round2(sum / float64(runs))
		w.Average = &v
	}
	return w
}

const exportFrom = ` FROM checklist_runs r JOIN scope s ON s.id=r.location_id
 JOIN checklist_template_versions v ON v.organization_id=r.organization_id AND v.id=r.template_version_id
 JOIN checklist_templates t ON t.organization_id=v.organization_id AND t.id=v.template_id
 WHERE`

// CountExportRows cuenta como máximo limit+1 filas para decidir si la exportación cabe.
func (s *store) CountExportRows(ctx context.Context, sc domain.Scope, limit int) (int, error) {
	var n int
	err := s.tx.QueryRow(ctx, `WITH `+scopeCTE+` SELECT count(*) FROM (SELECT 1`+exportFrom+runsFilter+` LIMIT $7) x`, append(periodArgs(sc), limit+1)...).Scan(&n)
	return n, err
}

// StreamExportRows recorre el historial con un cursor, sin cargarlo completo en memoria.
func (s *store) StreamExportRows(ctx context.Context, sc domain.Scope, fn func(domain.ExportRow) error) error {
	rows, err := s.tx.Query(ctx, `WITH `+scopeCTE+`
 SELECT r.id::text, (r.submitted_at AT TIME ZONE s.tz)::date, r.submitted_at, s.id::text, s.name, t.name, t.area, v.version_number, r.compliance_percent::float8`+exportFrom+runsFilter+`
 ORDER BY r.submitted_at, r.id`, periodArgs(sc)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v domain.ExportRow
		if err = rows.Scan(&v.RunID, &v.LocalDate, &v.SubmittedAt, &v.LocationID, &v.LocationName, &v.TemplateName, &v.Area, &v.TemplateVersion, &v.CompliancePercent); err != nil {
			return err
		}
		if err = fn(v); err != nil {
			return err
		}
	}
	return rows.Err()
}
