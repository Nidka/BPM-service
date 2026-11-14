package application

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/features/reports/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// Service reúne los casos de uso de reportes.
type Service struct {
	work      UnitOfWork
	verifier  CertificateVerifier
	now       func() time.Time
	exportMax int
	random    io.Reader
}

// New crea el servicio. verifier atiende la verificación pública de constancias.
func New(work UnitOfWork, verifier CertificateVerifier) *Service {
	return &Service{work: work, verifier: verifier, now: time.Now, exportMax: domain.MaxExportRows}
}

// authorize resuelve el rol real en la organización antes de cualquier lectura de negocio.
func (s *Service) authorize(ctx context.Context, db Store, user, org string, allowed func(domain.Actor) bool) (domain.Actor, error) {
	if !domain.ValidOrganization(org) {
		return domain.Actor{}, domain.ErrDenied
	}
	a, err := db.Actor(ctx, user, org)
	if err != nil {
		return a, err
	}
	if !allowed(a) {
		return a, domain.ErrDenied
	}
	return a, nil
}

func canReport(a domain.Actor) bool     { return a.CanReport() }
func canAdminister(a domain.Actor) bool { return a.CanAdminister() }

// PeriodInput es un período solicitado y una sede opcional.
type PeriodInput struct{ From, To, LocationID string }

// scopeFor valida el período y el formato de la sede; la pertenencia al alcance se verifica en requireLocation.
func scopeFor(a domain.Actor, org string, in PeriodInput, maxDays int) (domain.Scope, error) {
	p, err := domain.ParsePeriod(in.From, in.To, maxDays)
	if err != nil {
		return domain.Scope{}, err
	}
	if in.LocationID != "" && !domain.ValidID(in.LocationID) {
		return domain.Scope{}, domain.ErrLocationNotFound
	}
	return domain.Scope{OrganizationID: org, UserID: a.ID, AllLocations: a.AllLocations(), LocationID: strings.ToLower(in.LocationID), Period: p}, nil
}

// requireLocation exige que la sede pedida pertenezca al alcance del actor.
func requireLocation(ctx context.Context, db Store, scope domain.Scope) (map[string]string, error) {
	names, err := db.LocationNames(ctx, scope)
	if err != nil {
		return nil, err
	}
	if scope.LocationID != "" && len(names) == 0 {
		return nil, domain.ErrLocationNotFound
	}
	return names, nil
}

// Compliance es el RF-16: cumplimiento BPM por día, sede y empresa.
func (s *Service) Compliance(ctx context.Context, user, org string, in PeriodInput) (domain.ComplianceReport, error) {
	var out domain.ComplianceReport
	err := s.work.Within(ctx, func(db Store) error {
		a, err := s.authorize(ctx, db, user, org, canReport)
		if err != nil {
			return err
		}
		scope, err := scopeFor(a, org, in, domain.MaxComplianceDays)
		if err != nil {
			return err
		}
		if _, err = requireLocation(ctx, db, scope); err != nil {
			return err
		}
		rows, err := db.ComplianceRows(ctx, scope)
		if err != nil {
			return err
		}
		out = domain.BuildCompliance(scope.Period, rows)
		return nil
	})
	return out, err
}

// Summary es el RF-28: reporte de cumplimiento por empresa y período.
func (s *Service) Summary(ctx context.Context, user, org string, in PeriodInput) (domain.Summary, error) {
	var out domain.Summary
	err := s.work.Within(ctx, func(db Store) error {
		a, err := s.authorize(ctx, db, user, org, canReport)
		if err != nil {
			return err
		}
		scope, err := scopeFor(a, org, in, domain.MaxSummaryDays)
		if err != nil {
			return err
		}
		out, err = buildSummary(ctx, db, scope)
		return err
	})
	return out, err
}

func buildSummary(ctx context.Context, db Store, scope domain.Scope) (domain.Summary, error) {
	names, err := requireLocation(ctx, db, scope)
	if err != nil {
		return domain.Summary{}, err
	}
	comp, err := db.ComplianceRows(ctx, scope)
	if err != nil {
		return domain.Summary{}, err
	}
	templates, err := db.TemplateRows(ctx, scope)
	if err != nil {
		return domain.Summary{}, err
	}
	incidents, err := db.IncidentRows(ctx, scope)
	if err != nil {
		return domain.Summary{}, err
	}
	temps, err := db.TemperatureRows(ctx, scope)
	if err != nil {
		return domain.Summary{}, err
	}
	return domain.BuildSummary(scope.Period, comp, templates, incidents, temps, names), nil
}

// KPIs es el RF-30. El avance de capacitación solo se calcula para ADMIN y SUPER_ADMIN.
func (s *Service) KPIs(ctx context.Context, user, org string) (domain.KPIs, error) {
	var out domain.KPIs
	err := s.work.Within(ctx, func(db Store) error {
		a, err := s.authorize(ctx, db, user, org, canReport)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		scope := domain.Scope{OrganizationID: org, UserID: a.ID, AllLocations: a.AllLocations()}
		if out, err = db.KPIs(ctx, scope, now); err != nil {
			return err
		}
		out.GeneratedAt = now
		if a.CanAdminister() {
			t, err := db.TrainingKPI(ctx, org)
			if err != nil {
				return err
			}
			out.Training = &t
		}
		return nil
	})
	return out, err
}

// TrainingCourses es el RF-09 por curso.
func (s *Service) TrainingCourses(ctx context.Context, user, org string) ([]domain.TrainingCourseStat, error) {
	var out []domain.TrainingCourseStat
	err := s.work.Within(ctx, func(db Store) error {
		if _, err := s.authorize(ctx, db, user, org, canAdminister); err != nil {
			return err
		}
		var err error
		out, err = db.TrainingCourses(ctx, org)
		return err
	})
	return out, err
}

// TrainingPeople es el RF-09 por persona, paginado por cursor.
func (s *Service) TrainingPeople(ctx context.Context, user, org, courseID string, page pagination.Request) (pagination.Page[domain.TrainingPerson], error) {
	var out pagination.Page[domain.TrainingPerson]
	err := s.work.Within(ctx, func(db Store) error {
		if _, err := s.authorize(ctx, db, user, org, canAdminister); err != nil {
			return err
		}
		if courseID != "" {
			if !domain.ValidID(courseID) {
				return domain.ErrCourseNotFound
			}
			courseID = strings.ToLower(courseID)
			ok, err := db.CourseExists(ctx, org, courseID)
			if err != nil {
				return err
			}
			if !ok {
				return domain.ErrCourseNotFound
			}
		}
		w, err := pagination.Open(page, user, org, "reports.training.people", courseID)
		if err != nil {
			return err
		}
		rows, err := db.TrainingPeople(ctx, org, courseID, w)
		if err != nil {
			return err
		}
		out = pagination.Close(rows, w, func(v domain.TrainingPerson) pagination.Position { return pagination.Position{ID: v.UserID} }, user, org, "reports.training.people", courseID)
		return nil
	})
	return out, err
}

// Settings devuelve la configuración vigente (o los valores por defecto).
func (s *Service) Settings(ctx context.Context, user, org string) (domain.Settings, error) {
	var out domain.Settings
	err := s.work.Within(ctx, func(db Store) error {
		if _, err := s.authorize(ctx, db, user, org, canAdminister); err != nil {
			return err
		}
		var err error
		out, err = db.Settings(ctx, org)
		return err
	})
	return out, err
}

// UpdateSettings reemplaza la configuración de reportes de la empresa.
func (s *Service) UpdateSettings(ctx context.Context, user, org string, in domain.Settings) (domain.Settings, error) {
	err := s.work.Within(ctx, func(db Store) error {
		if _, err := s.authorize(ctx, db, user, org, canAdminister); err != nil {
			return err
		}
		if err := domain.ValidateSettings(in); err != nil {
			return err
		}
		if err := db.SaveSettings(ctx, org, user, in); err != nil {
			return err
		}
		return db.Audit(ctx, org, user, "REPORT_SETTINGS_UPDATED", "REPORT_SETTINGS", org)
	})
	return in, err
}

// ExportSink recibe la exportación: Begin se invoca una vez, tras validar y contar, y antes de
// la primera fila; así el adaptador solo escribe cabeceras HTTP cuando la exportación procede.
type ExportSink interface {
	Begin() error
	Row(domain.ExportRow) error
}

// ExportRuns es el RF-17: exporta el historial de ejecuciones finalizadas en streaming.
func (s *Service) ExportRuns(ctx context.Context, user, org string, in PeriodInput, sink ExportSink) error {
	return s.work.Within(ctx, func(db Store) error {
		a, err := s.authorize(ctx, db, user, org, canReport)
		if err != nil {
			return err
		}
		scope, err := scopeFor(a, org, in, domain.MaxExportDays)
		if err != nil {
			return err
		}
		if _, err = requireLocation(ctx, db, scope); err != nil {
			return err
		}
		n, err := db.CountExportRows(ctx, scope, s.exportMax)
		if err != nil {
			return err
		}
		if n > s.exportMax {
			return domain.ErrExportTooLarge
		}
		entity := scope.Period.From.Format(domain.DateLayout) + ".." + scope.Period.To.Format(domain.DateLayout)
		if err = db.Audit(ctx, org, user, "CHECKLIST_HISTORY_EXPORTED", "CHECKLIST_RUN", entity); err != nil {
			return err
		}
		if err = sink.Begin(); err != nil {
			return err
		}
		return db.StreamExportRows(ctx, scope, sink.Row)
	})
}

// WithExportLimit reduce el máximo de filas exportables (nunca por encima de domain.MaxExportRows).
func (s *Service) WithExportLimit(n int) *Service {
	if n > 0 && n < domain.MaxExportRows {
		s.exportMax = n
	}
	return s
}
