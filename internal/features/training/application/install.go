package application

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/catalog"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// InstalledCourse es el resultado de instalar la ruta de un módulo en una organización.
type InstalledCourse struct {
	CourseCode string
	// Skipped es true cuando la versión publicada ya usa la versión vigente del banco (la instalación es idempotente).
	Skipped       bool
	Created       bool
	VersionNumber int
	Units, Items  int
	// VideoKept indica que la primera unidad conserva el video de la versión anterior.
	VideoKept bool
}

// InstallBPMCatalog instala (o actualiza) en una organización los cinco módulos BPM con la ruta de aprendizaje en
// unidades y los 100 ítems del banco oficial. Es idempotente: un módulo cuya versión publicada ya usa el banco
// vigente no se toca. Si el módulo ya existe, publica una versión nueva (la anterior queda ARCHIVED, sus intentos y
// constancias no cambian) y conserva el video de su primera lección; si no existe, lo crea. Todo ocurre en una
// transacción. dryRun calcula el resultado sin guardar. Es una operación de plataforma: no hay un actor de
// organización, solo la persona o proceso (by) que se registra en la auditoría.
func (s *Service) InstallBPMCatalog(ctx context.Context, org, by string, dryRun bool) ([]InstalledCourse, error) {
	if !domain.ValidOrganization(org) {
		return nil, domain.ErrCourseNotFound
	}
	bank, courses := catalog.MustLoad()
	var report []InstalledCourse
	err := s.work.Within(ctx, func(db Store) error {
		report = report[:0]
		for _, module := range courses.Modules {
			item, e := installModule(ctx, db, org, by, s.now().UTC(), bank, courses, module)
			if e != nil {
				return fmt.Errorf("%s: %w", module.CourseCode, e)
			}
			report = append(report, item)
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if err == errDryRun {
		return report, nil
	}
	return report, err
}

type dryRunError struct{}

func (dryRunError) Error() string { return "dry run" }

var errDryRun error = dryRunError{}

func installModule(ctx context.Context, db Store, org, by string, now time.Time, bank *catalog.Bank, courses *catalog.Courses, module catalog.CourseModule) (InstalledCourse, error) {
	report := InstalledCourse{CourseCode: module.CourseCode, Units: len(module.Units)}
	course, found, err := db.CourseByCode(ctx, org, module.CourseCode)
	if err != nil {
		return report, err
	}
	if !found {
		minutes := courses.EstimatedMinutes
		if course, err = db.CreateCourse(ctx, org, by, domain.NewCourse{Code: module.CourseCode, Title: module.Title, Description: module.Description, EstimatedDurationMinutes: &minutes}); err != nil {
			return report, err
		}
		report.Created = true
	}
	if course, err = db.LockCourse(ctx, org, course.ID); err != nil {
		return report, err
	}
	if installed, e := db.PublishedBankVersion(ctx, org, course.ID); e != nil {
		return report, e
	} else if installed >= bank.Version {
		report.Skipped = true
		return report, nil
	}
	draft, found, err := db.Draft(ctx, org, course)
	if err != nil {
		return report, err
	}
	if !found {
		if draft, err = db.CreateDraft(ctx, org, by, course); err != nil {
			return report, err
		}
	}
	lessons, questions, err := bankContent(bank, module, draft.Lessons)
	if err != nil {
		return report, err
	}
	report.VideoKept = lessons[0].VideoMediaID != "" || lessons[0].ExternalVideo != nil
	report.Items = len(questions)
	input, err := domain.NormalizeDraft(domain.DraftInput{VersionID: draft.Version.ID, PassingScore: bank.PassScorePercent,
		RowVersion: draft.Version.RowVersion, Lessons: lessons, Questions: questions})
	if err != nil {
		return report, err
	}
	if draft, err = db.ReplaceDraft(ctx, org, by, draft, input); err != nil {
		return report, err
	}
	statuses := map[string]string{}
	if ids := videoIDs(draft.Lessons); len(ids) > 0 {
		if statuses, err = db.VideoStatuses(ctx, org, ids); err != nil {
			return report, err
		}
	}
	if err = draft.ValidateForPublish(statuses); err != nil {
		return report, err
	}
	_, version, err := db.PublishDraft(ctx, org, by, draft, now)
	if err != nil {
		return report, err
	}
	report.VersionNumber = version.Number
	return report, db.Audit(ctx, org, by, "training.catalog.installed", "training_course", course.ID)
}

// bankContent arma lecciones (una por unidad) y preguntas (los ítems de cada unidad) con identificadores nuevos.
// La primera unidad conserva el video de la primera lección de la versión que se reemplaza.
func bankContent(bank *catalog.Bank, module catalog.CourseModule, previous []domain.Lesson) ([]domain.Lesson, []domain.Question, error) {
	var lessons []domain.Lesson
	var questions []domain.Question
	for i, unit := range module.Units {
		id, err := domain.NewUUID(rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		lesson := domain.Lesson{ID: id, Title: unit.Title, Content: unit.Content, DisplayOrder: i + 1, KeyPoints: unit.KeyPoints}
		if i == 0 && len(previous) > 0 {
			lesson.VideoMediaID, lesson.ExternalVideo = previous[0].VideoMediaID, previous[0].ExternalVideo
		}
		lessons = append(lessons, lesson)
		for _, code := range unit.Items {
			item, ok := bank.Item(code)
			if !ok {
				return nil, nil, fmt.Errorf("ítem %s no existe en el banco", code)
			}
			qid, err := domain.NewUUID(rand.Reader)
			if err != nil {
				return nil, nil, err
			}
			questions = append(questions, domain.Question{ID: qid, LessonID: id, Text: item.Text, Options: item.OptionTexts(),
				CorrectOption: item.KeyIndex(), Explanation: item.Justification, DisplayOrder: len(questions) + 1,
				ItemCode: item.Code, BankVersion: bank.Version, Bloom: item.Bloom, Sources: item.Sources})
		}
	}
	return lessons, questions, nil
}
