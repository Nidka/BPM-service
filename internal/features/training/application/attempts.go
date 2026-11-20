package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// AttemptOutcome es el resultado de enviar un intento. Replayed es true cuando
// la solicitud repite exactamente un intento ya registrado (HTTP 200 en vez de 201).
type AttemptOutcome struct {
	ID, AttemptType string
	Score           int
	Passed          bool
	CompletedAt     time.Time
	Results         []domain.Result
	Progress        domain.Progress
	Certificate     *domain.Certificate
	// Achievements son las insignias otorgadas por este intento (también en la repetición idéntica).
	Achievements []domain.Achievement
	// XPEarned es el XP del intento (curso + módulo + puntos de las insignias otorgadas);
	// nil en intentos anteriores a V15, que no guardaron el XP.
	XPEarned *int
	// NextCourse es el siguiente curso sugerido tras aprobar un FINAL; nil en otro caso.
	NextCourse *domain.NextCourse
	Replayed   bool
}

// attemptHash identifica el contenido del intento (sin depender del orden de las
// respuestas ya normalizadas) para distinguir reintentos de reutilización indebida.
func attemptHash(user, org, courseID string, in domain.AttemptInput) (string, error) {
	raw, err := json.Marshal(struct {
		Kind, User, Org, Course string
		Input                   domain.AttemptInput
	}{"training.attempt", user, org, courseID, in})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// SubmitAttempt califica en el servidor un intento DIAGNOSTIC o FINAL.
// El cliente nunca aporta puntaje, aprobación ni XP. Un reintento idéntico
// (mismo id, clientOperationId y contenido) devuelve el mismo resultado; el mismo
// identificador con otro contenido es ErrIdempotencyConflict.
func (s *Service) SubmitAttempt(ctx context.Context, user, org, courseID string, in domain.AttemptInput) (out AttemptOutcome, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	now := s.now().UTC()
	in, err = domain.NormalizeAttempt(in, now)
	if err != nil {
		return out, err
	}
	hash, err := attemptHash(user, org, courseID, in)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, persister); e != nil {
			return e
		}
		if e := db.LockLearner(ctx, org, user, courseID); e != nil {
			return e
		}
		// El replay se resuelve antes de mirar la versión vigente: un reintento tardío
		// tras una nueva publicación debe devolver el intento original, no un 409.
		previous, e := db.FindAttempt(ctx, org, in.ID, in.ClientOperationID)
		if e != nil {
			return e
		}
		if len(previous) > 0 {
			return s.replay(ctx, db, org, user, courseID, hash, in, previous, &out)
		}
		version, e := db.PublishedVersion(ctx, org, courseID)
		if e != nil {
			return e
		}
		if in.VersionID != version.ID {
			return domain.ErrVersionOutdated
		}
		questions, e := db.Questions(ctx, org, version.ID)
		if e != nil {
			return e
		}
		if in.AttemptType == domain.AttemptModule {
			// Una lección ajena (de otra versión, curso u organización) responde 404 y
			// el módulo solo evalúa las preguntas de esa lección.
			if e = db.LessonInVersion(ctx, org, version.ID, in.LessonID); e != nil {
				return e
			}
			if questions = domain.LessonQuestions(questions, in.LessonID); len(questions) == 0 {
				return &domain.ValidationError{Fields: map[string]string{"lessonId": "La lección no tiene preguntas para evaluar."}}
			}
		}
		grade, e := domain.GradeAttempt(questions, in.Answers, version.PassingScore)
		if e != nil {
			return e
		}
		current, e := db.Progress(ctx, org, user, courseID)
		if e != nil {
			return e
		}
		updated := current.AfterAttempt(in.AttemptType, grade, now)
		// El XP del intento es lo que suma sobre lo que la persona ya tenía (mejor resultado).
		previousModuleXP := 0
		if in.AttemptType == domain.AttemptModule {
			if previousModuleXP, e = db.ModuleXP(ctx, org, user, in.LessonID); e != nil {
				return e
			}
		}
		xp := domain.AttemptXP(in.AttemptType, grade, current, updated, previousModuleXP)
		record := AttemptRecord{ID: in.ID, ClientOperationID: in.ClientOperationID, CourseID: courseID, VersionID: version.ID, UserID: user,
			AttemptType: in.AttemptType, LessonID: in.LessonID, RequestHash: hash, Score: grade.Score, Passed: grade.Passed,
			StartedAt: in.StartedAt, ClientCreatedAt: in.ClientCreatedAt, CompletedAt: now.Truncate(time.Microsecond), XPAwarded: &xp}
		if e = db.InsertAttempt(ctx, org, record, grade.Results); e != nil {
			return e
		}
		if e = db.SaveProgress(ctx, org, user, courseID, updated); e != nil {
			return e
		}
		if in.AttemptType == domain.AttemptModule {
			// MODULE no completa el curso ni emite constancia; solo da XP por módulo (mejor resultado).
			moduleXP := 0
			if grade.Passed {
				moduleXP = grade.CorrectCount * domain.XPPerCorrectModule
			}
			if e = db.SaveModuleResult(ctx, org, user, courseID, in.LessonID, grade.Score, grade.Passed, moduleXP); e != nil {
				return e
			}
			if grade.Passed {
				// Aprobar el módulo completa la lección (idempotente: repetir no cambia la fecha).
				if e = db.CompleteLesson(ctx, org, user, courseID, in.LessonID, domain.LessonModulePassed); e != nil {
					return e
				}
			}
		}
		if in.AttemptType == domain.AttemptFinal && grade.Passed {
			code, e := domain.NewVerificationCode(s.random)
			if e != nil {
				return e
			}
			if e = db.IssueCertificate(ctx, org, user, version.ID, in.ID, code); e != nil {
				return e
			}
		}
		updated, e = s.withLessons(ctx, db, org, user, version.ID, updated)
		if e != nil {
			return e
		}
		certificate, e := db.LatestCertificate(ctx, org, user, courseID)
		if e != nil {
			return e
		}
		awarded, e := s.awardAchievements(ctx, db, org, user, in.ID)
		if e != nil {
			return e
		}
		var next *domain.NextCourse
		if in.AttemptType == domain.AttemptFinal && grade.Passed {
			if next, e = s.nextCourse(ctx, db, org, user, courseID, now); e != nil {
				return e
			}
		}
		earned := xp + achievementPoints(awarded)
		out = AttemptOutcome{ID: in.ID, AttemptType: in.AttemptType, Score: grade.Score, Passed: grade.Passed,
			CompletedAt: record.CompletedAt, Results: grade.Results, Progress: updated, Certificate: certificate, Achievements: awarded,
			XPEarned: &earned, NextCourse: next}
		return db.Audit(ctx, org, user, "training.attempt."+in.AttemptType, "training_attempt", in.ID)
	})
	return out, err
}

// replay reconstruye la respuesta de un intento previo. Los intentos y las versiones
// publicadas son inmutables, por lo que el resultado es idéntico al original; el
// progreso y la constancia reflejan el estado actual del usuario.
func (s *Service) replay(ctx context.Context, db Store, org, user, courseID, hash string, in domain.AttemptInput, previous []AttemptRecord, out *AttemptOutcome) error {
	if len(previous) != 1 || previous[0].RequestHash != hash || previous[0].UserID != user || previous[0].ID != in.ID {
		return domain.ErrIdempotencyConflict
	}
	record := previous[0]
	results, err := db.AttemptResults(ctx, org, record.ID)
	if err != nil {
		return err
	}
	progress, err := db.Progress(ctx, org, user, courseID)
	if err != nil {
		return err
	}
	// El avance por lección se calcula contra la versión vigente; si el curso ya no está
	// publicado se devuelve el progreso básico.
	if version, e := db.PublishedVersion(ctx, org, courseID); e == nil {
		if progress, err = s.withLessons(ctx, db, org, user, version.ID, progress); err != nil {
			return err
		}
	} else if !errors.Is(e, domain.ErrCourseNotFound) {
		return e
	}
	certificate, err := db.LatestCertificate(ctx, org, user, courseID)
	if err != nil {
		return err
	}
	awarded, err := db.AttemptAchievements(ctx, org, record.ID)
	if err != nil {
		return err
	}
	var earned *int
	if record.XPAwarded != nil {
		total := *record.XPAwarded + achievementPoints(awarded)
		earned = &total
	}
	var next *domain.NextCourse
	if record.AttemptType == domain.AttemptFinal && record.Passed {
		if next, err = s.nextCourse(ctx, db, org, user, courseID, s.now().UTC()); err != nil {
			return err
		}
	}
	*out = AttemptOutcome{ID: record.ID, AttemptType: record.AttemptType, Score: record.Score, Passed: record.Passed,
		CompletedAt: record.CompletedAt, Results: results, Progress: progress, Certificate: certificate, Achievements: awarded,
		XPEarned: earned, NextCourse: next, Replayed: true}
	return nil
}

// achievementPoints suma los puntos de las insignias otorgadas por un intento.
func achievementPoints(items []domain.Achievement) int {
	total := 0
	for _, a := range items {
		total += a.Points
	}
	return total
}
