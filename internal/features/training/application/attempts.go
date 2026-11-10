package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	Replayed        bool
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
		grade, e := domain.GradeAttempt(questions, in.Answers, version.PassingScore)
		if e != nil {
			return e
		}
		record := AttemptRecord{ID: in.ID, ClientOperationID: in.ClientOperationID, CourseID: courseID, VersionID: version.ID, UserID: user,
			AttemptType: in.AttemptType, RequestHash: hash, Score: grade.Score, Passed: grade.Passed,
			StartedAt: in.StartedAt, ClientCreatedAt: in.ClientCreatedAt, CompletedAt: now.Truncate(time.Microsecond)}
		if e = db.InsertAttempt(ctx, org, record, grade.Results); e != nil {
			return e
		}
		current, e := db.Progress(ctx, org, user, courseID)
		if e != nil {
			return e
		}
		updated := current.AfterAttempt(in.AttemptType, grade, now)
		if e = db.SaveProgress(ctx, org, user, courseID, updated); e != nil {
			return e
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
		certificate, e := db.LatestCertificate(ctx, org, user, courseID)
		if e != nil {
			return e
		}
		out = AttemptOutcome{ID: in.ID, AttemptType: in.AttemptType, Score: grade.Score, Passed: grade.Passed,
			CompletedAt: record.CompletedAt, Results: grade.Results, Progress: updated, Certificate: certificate}
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
	certificate, err := db.LatestCertificate(ctx, org, user, courseID)
	if err != nil {
		return err
	}
	*out = AttemptOutcome{ID: record.ID, AttemptType: record.AttemptType, Score: record.Score, Passed: record.Passed,
		CompletedAt: record.CompletedAt, Results: results, Progress: progress, Certificate: certificate, Replayed: true}
	return nil
}
