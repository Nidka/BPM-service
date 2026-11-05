package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
)

type Store interface {
	StartedRun(context.Context, string, string) (bool, error)
	RunSchedule(context.Context, domain.Actor, string, domain.Run) (domain.Assignment, error)
	PeriodRuns(context.Context, string, []domain.AssignmentPeriod) (map[string]domain.AssignedRun, error)
	RunHistory(context.Context, domain.Actor, string, pagination.Window, domain.HistoryFilter) ([]domain.RunSummary, error)
	Actor(context.Context, string, string) (domain.Actor, error)
	LocationAccess(context.Context, domain.Actor, string, string) error
	CatalogPage(context.Context, string, pagination.Window, string) ([]domain.Template, error)
	Catalog(context.Context, string) ([]domain.Template, error)
	CreateTemplate(context.Context, domain.Actor, string, domain.Template) error
	CreateRun(context.Context, domain.Actor, domain.Run) (domain.Run, error)
	Run(context.Context, domain.Actor, string, string, bool) (domain.Run, error)
	Replay(context.Context, string, string, string) (domain.Run, bool, error)
	SaveAnswers(context.Context, domain.Actor, domain.Run, []domain.Answer, string, string) (domain.Run, error)
	Submit(context.Context, domain.Actor, domain.Run, float64, string, string) (domain.Run, error)
}

type UnitOfWork interface {
	Within(context.Context, func(Store) error) error
}

type Service struct {
	work UnitOfWork
	now  func() time.Time
}

func New(work UnitOfWork) *Service { return &Service{work: work, now: time.Now} }

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validateText(fields map[string]string, field, value string, min, max int) {
	if n := utf8.RuneCountInString(value); n < min || n > max || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		fields[field] = "Revisa este campo: es obligatorio o supera la longitud permitida."
	}
}

func validDecimal(v *float64) bool {
	return v == nil || (!math.IsNaN(*v) && !math.IsInf(*v, 0) && math.Abs(*v) < 100000000 && math.Abs(*v*100-math.Round(*v*100)) < 0.000001)
}

func (s *Service) Catalog(ctx context.Context, uid, org string) (out []domain.Template, err error) {
	out = []domain.Template{}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := db.Actor(ctx, uid, org); e != nil {
			return e
		}
		var e error
		out, e = db.Catalog(ctx, org)
		return e
	})
	return
}

func (s *Service) CreateTemplate(ctx context.Context, uid, org string, v domain.Template) (domain.Template, error) {
	v.Name = strings.TrimSpace(v.Name)
	v.Area = strings.TrimSpace(v.Area)
	v.Description = strings.TrimSpace(v.Description)
	fields := map[string]string{}
	if !uuid.MatchString(v.ID) || !uuid.MatchString(v.VersionID) || v.ID == v.VersionID {
		fields["id"] = "Identificadores UUID distintos son obligatorios."
	}
	validateText(fields, "name", v.Name, 2, 180)
	validateText(fields, "area", v.Area, 2, 80)
	validateText(fields, "description", v.Description, 0, 500)
	if len(v.Items) == 0 || len(v.Items) > 100 {
		fields["items"] = "Incluye entre 1 y 100 preguntas."
	}
	seenIDs, seenOrders := map[string]bool{}, map[int]bool{}
	for i := range v.Items {
		item := &v.Items[i]
		item.SectionName = strings.TrimSpace(item.SectionName)
		item.Label = strings.TrimSpace(item.Label)
		item.Unit = strings.TrimSpace(item.Unit)
		if !uuid.MatchString(item.ID) || seenIDs[item.ID] || item.ID == v.ID || item.ID == v.VersionID ||
			item.DisplayOrder <= 0 || seenOrders[item.DisplayOrder] ||
			utf8.RuneCountInString(item.SectionName) < 2 || utf8.RuneCountInString(item.SectionName) > 120 ||
			utf8.RuneCountInString(item.Label) < 2 || utf8.RuneCountInString(item.Label) > 300 ||
			strings.IndexFunc(item.Label+item.SectionName+item.Unit, unicode.IsControl) >= 0 || len(item.Unit) > 20 {
			fields["items"] = "Revisa los identificadores, orden y textos de las preguntas."
		}
		seenIDs[item.ID], seenOrders[item.DisplayOrder] = true, true
		switch item.ResponseType {
		case "COMPLIANCE", "TEXT":
			if item.MinimumValue != nil || item.MaximumValue != nil || item.Unit != "" {
				fields["items"] = "Las preguntas de cumplimiento o texto no usan rangos numéricos."
			}
		case "TEMPERATURE", "NUMBER":
			if item.MinimumValue == nil || item.MaximumValue == nil || !validDecimal(item.MinimumValue) || !validDecimal(item.MaximumValue) ||
				(item.MinimumValue != nil && item.MaximumValue != nil && *item.MinimumValue > *item.MaximumValue) {
				fields["items"] = "Las preguntas numéricas requieren un rango válido de dos decimales."
			}
		default:
			fields["items"] = "Tipo de respuesta no permitido."
		}
		if item.EvidenceRequiredOnFailure && item.ResponseType == "TEXT" {
			fields["items"] = "La foto por incumplimiento se aplica a respuestas de cumplimiento o mediciones."
		}
	}
	if len(fields) != 0 {
		return domain.Template{}, &domain.ValidationError{Fields: fields}
	}
	v.VersionNumber = 1
	err := s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanManageTemplates(actor.Role) {
			return domain.ErrDenied
		}
		return db.CreateTemplate(ctx, actor, org, v)
	})
	return v, err
}

func (s *Service) CreateRun(ctx context.Context, uid, org string, v domain.Run) (domain.Run, error) {
	fields := map[string]string{}
	if v.AssignmentID != "" && !uuid.MatchString(v.AssignmentID) {
		fields["assignmentId"] = "Selecciona un control programado válido."
	}
	for name, id := range map[string]string{"id": v.ID, "locationId": v.LocationID, "templateVersionId": v.TemplateVersionID, "clientOperationId": v.ClientOperationID} {
		if !uuid.MatchString(id) {
			fields[name] = "Ingresa un UUID válido."
		}
	}
	if v.ClientCreatedAt.IsZero() || v.ClientCreatedAt.After(s.now().Add(5*time.Minute)) {
		fields["clientCreatedAt"] = "Ingresa una fecha válida que no esté en el futuro."
	}
	if len(fields) > 0 {
		return domain.Run{}, &domain.ValidationError{Fields: fields}
	}
	v.ClientCreatedAt = v.ClientCreatedAt.UTC().Truncate(time.Microsecond)
	v.AssignmentID = strings.ToLower(v.AssignmentID)
	v.OrganizationID = org
	v.PerformedBy = uid
	v.Status = "DRAFT"
	v.RowVersion = 1
	err := s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanPerformRuns(actor.Role) {
			return domain.ErrDenied
		}
		if e = db.LocationAccess(ctx, actor, org, v.LocationID); e != nil {
			return e
		}
		exists, e := db.StartedRun(ctx, org, v.ID)
		if e != nil {
			return e
		}
		if !exists {
			schedule, scheduleErr := db.RunSchedule(ctx, actor, org, v)
			if scheduleErr != nil {
				return scheduleErr
			}
			// La fecha del registro y su período usan el mismo instante, tras obtener el bloqueo.
			v.CreatedAt = s.now().UTC().Truncate(time.Microsecond)
			if schedule.ID != "" {
				period, periodErr := schedule.PeriodAt(v.CreatedAt)
				if periodErr != nil {
					return periodErr
				}
				started, readErr := db.PeriodRuns(ctx, org, []domain.AssignmentPeriod{period})
				if readErr != nil {
					return readErr
				}
				if _, found := started[schedule.ID]; found {
					return domain.ErrAssignmentStarted
				}
			}
		}
		v, e = db.CreateRun(ctx, actor, v)
		return e
	})
	return v, err
}

func (s *Service) Run(ctx context.Context, uid, org, runID string) (v domain.Run, err error) {
	if !uuid.MatchString(runID) {
		return v, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanSuperviseRuns(actor.Role) {
			return domain.ErrDenied
		}
		v, e = db.Run(ctx, actor, org, runID, false)
		return e
	})
	return
}

func validKey(key string) bool {
	return len(key) >= 8 && len(key) <= 100 && strings.IndexFunc(key, func(r rune) bool { return r < 33 || r > 126 }) < 0
}

func requestHash(input any) string {
	data, _ := json.Marshal(input)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) SaveAnswers(ctx context.Context, uid, org, runID string, version int, key string, answers []domain.Answer) (v domain.Run, err error) {
	fields := map[string]string{}
	if !uuid.MatchString(runID) {
		fields["runId"] = "Ingresa un UUID válido."
	}
	if version < 1 {
		fields["rowVersion"] = "La versión debe ser positiva."
	}
	if !validKey(key) {
		fields["idempotencyKey"] = "Envía una clave única de 8 a 100 caracteres visibles."
	}
	if len(answers) > 100 {
		fields["answers"] = "Envía como máximo 100 respuestas."
	}
	seen := map[string]bool{}
	for _, a := range answers {
		if !uuid.MatchString(a.ItemID) || seen[a.ItemID] || len(a.TextValue) > 2000 || len(a.Observation) > 2000 ||
			strings.IndexFunc(a.TextValue+a.Observation, unicode.IsControl) >= 0 || !validDecimal(a.NumericValue) {
			fields["answers"] = "Revisa los identificadores, textos y valores de las respuestas."
		}
		seen[a.ItemID] = true
	}
	if len(fields) > 0 {
		return v, &domain.ValidationError{Fields: fields}
	}
	slices.SortFunc(answers, func(a, b domain.Answer) int { return strings.Compare(a.ItemID, b.ItemID) })
	hash := requestHash(struct {
		Actor, RunID string
		Version      int
		Answers      []domain.Answer
	}{uid, runID, version, answers})
	key = "answers:" + key
	err = s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanPerformRuns(actor.Role) {
			return domain.ErrDenied
		}
		v, e = db.Run(ctx, actor, org, runID, true)
		if e != nil {
			return e
		}
		if v.PerformedBy != actor.ID {
			return domain.ErrDenied
		}
		if replay, found, replayErr := db.Replay(ctx, org, key, hash); replayErr != nil || found {
			v = replay
			return replayErr
		}
		if v.Status != "DRAFT" {
			return domain.ErrConflict
		}
		if v.RowVersion != version {
			return domain.ErrVersionConflict
		}
		items := map[string]domain.Item{}
		for _, item := range v.Items {
			items[item.ID] = item
		}
		for _, a := range answers {
			item, ok := items[a.ItemID]
			if !ok || !validAnswer(item, a) {
				return &domain.ValidationError{Fields: map[string]string{"answers": "Una respuesta no corresponde a la plantilla o a su tipo."}}
			}
		}
		v, e = db.SaveAnswers(ctx, actor, v, answers, key, hash)
		return e
	})
	return
}

func validAnswer(item domain.Item, a domain.Answer) bool {
	switch item.ResponseType {
	case "COMPLIANCE":
		return a.NumericValue == nil && a.TextValue == "" && (a.ComplianceResult == "COMPLIANT" || a.ComplianceResult == "NON_COMPLIANT" || a.ComplianceResult == "NOT_APPLICABLE")
	case "TEXT":
		return a.NumericValue == nil && a.ComplianceResult == "" && strings.TrimSpace(a.TextValue) != ""
	case "TEMPERATURE", "NUMBER":
		return a.NumericValue != nil && a.ComplianceResult == "" && a.TextValue == ""
	default:
		return false
	}
}

func (s *Service) Submit(ctx context.Context, uid, org, runID string, version int, key string) (v domain.Run, err error) {
	fields := map[string]string{}
	if !uuid.MatchString(runID) {
		fields["runId"] = "Ingresa un UUID válido."
	}
	if version < 1 {
		fields["rowVersion"] = "La versión debe ser positiva."
	}
	if !validKey(key) {
		fields["idempotencyKey"] = "Envía una clave única de 8 a 100 caracteres visibles."
	}
	if len(fields) > 0 {
		return v, &domain.ValidationError{Fields: fields}
	}
	hash := requestHash(struct {
		Actor, RunID string
		Version      int
	}{uid, runID, version})
	key = "submit:" + key
	err = s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, uid, org)
		if e != nil {
			return e
		}
		if !domain.CanPerformRuns(actor.Role) {
			return domain.ErrDenied
		}
		v, e = db.Run(ctx, actor, org, runID, true)
		if e != nil {
			return e
		}
		if v.PerformedBy != actor.ID {
			return domain.ErrDenied
		}
		if replay, found, replayErr := db.Replay(ctx, org, key, hash); replayErr != nil || found {
			v = replay
			return replayErr
		}
		if v.Status != "DRAFT" {
			return domain.ErrConflict
		}
		if v.RowVersion != version {
			return domain.ErrVersionConflict
		}
		answerByItem := map[string]domain.Answer{}
		for _, a := range v.Answers {
			answerByItem[a.ItemID] = a
		}
		var scored, passed int
		for _, item := range v.Items {
			a, ok := answerByItem[item.ID]
			if !ok {
				if item.Required {
					return &domain.ValidationError{Fields: map[string]string{"answers": "Completa todas las preguntas obligatorias antes de enviar."}}
				}
				continue
			}
			if !validAnswer(item, a) {
				return &domain.ValidationError{Fields: map[string]string{"answers": "Una respuesta dejó de ser válida."}}
			}
			if item.EvidenceRequiredOnFailure && (a.ComplianceResult == "NON_COMPLIANT" ||
				(a.NumericValue != nil && item.MinimumValue != nil && item.MaximumValue != nil &&
					(*a.NumericValue < *item.MinimumValue || *a.NumericValue > *item.MaximumValue))) && v.EvidenceCounts[item.ID] < 1 {
				return &domain.ValidationError{Fields: map[string]string{"answers": "Adjunta y confirma al menos una foto en cada pregunta que no cumple y solicita evidencia."}}
			}
			switch item.ResponseType {
			case "COMPLIANCE":
				if a.ComplianceResult == "NOT_APPLICABLE" {
					continue
				}
				scored++
				if a.ComplianceResult == "COMPLIANT" {
					passed++
				}
			case "TEMPERATURE", "NUMBER":
				scored++
				if item.MinimumValue != nil && item.MaximumValue != nil && *a.NumericValue >= *item.MinimumValue && *a.NumericValue <= *item.MaximumValue {
					passed++
				}
			}
		}
		if scored == 0 {
			return &domain.ValidationError{Fields: map[string]string{"answers": "Incluye al menos una respuesta evaluable."}}
		}
		score := math.Round(float64(passed)*10000/float64(scored)) / 100
		v, e = db.Submit(ctx, actor, v, score, key, hash)
		return e
	})
	return
}
