package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Nidka/BPM-service/internal/features/research/domain"
)

// member exige una membresía ACTIVE: el participante siempre actúa como miembro
// de una organización; el tenant sale de la membresía, nunca del cliente.
func (s *Service) member(ctx context.Context, db Store, user, org string) error {
	ok, err := db.IsMember(ctx, user, org)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotMember
	}
	return nil
}

// Participation devuelve el estado del participante: consentimiento, documento
// vigente y, solo con consentimiento vigente, la encuesta CAP publicada.
func (s *Service) Participation(ctx context.Context, user, org string) (out Participation, err error) {
	err = s.work.Within(ctx, func(db Store) error {
		if e := s.member(ctx, db, user, org); e != nil {
			return e
		}
		var e error
		out, e = s.view(ctx, db, user, org)
		return e
	})
	return out, err
}

// view arma el estado del participante dentro de una transacción ya autorizada.
func (s *Service) view(ctx context.Context, db Store, user, org string) (Participation, error) {
	var out Participation
	doc, err := db.ActiveConsentDocument(ctx)
	if err != nil {
		return out, err
	}
	out.Document = doc
	out.Participant, err = db.FindParticipant(ctx, org, user, false)
	if err != nil || out.Participant == nil {
		return out, err
	}
	if out.Consent, err = db.LatestConsent(ctx, org, out.Participant.ID); err != nil {
		return out, err
	}
	if out.Submissions, err = db.Submissions(ctx, org, out.Participant.ID); err != nil {
		return out, err
	}
	if doc != nil {
		out.ConsentValid = out.Consent.ValidFor(doc.Version)
	}
	if !out.ConsentValid {
		return out, nil
	}
	survey, err := db.PublishedCAP(ctx)
	if err != nil || survey == nil {
		return out, err
	}
	out.Survey = &SurveyForm{InstrumentCode: survey.Instrument.Code, Name: survey.Instrument.Name,
		VersionID: survey.Version.ID, VersionNumber: survey.Version.Number, Questions: survey.Questions}
	return out, nil
}

// RecordConsent acepta o revoca el consentimiento informado. Aceptar exige el
// documento vigente (ErrConsentVersionStale si es otra versión) y crea al
// participante con un código seudónimo generado por el servidor. Revocar marca al
// participante como WITHDRAWN: no puede responder y sus datos salen de los
// resultados y exportaciones. Repetir el mismo estado no crea otro registro.
func (s *Service) RecordConsent(ctx context.Context, user, org, documentVersion string, accepted bool) (out Participation, err error) {
	err = s.work.Within(ctx, func(db Store) error {
		if e := s.member(ctx, db, user, org); e != nil {
			return e
		}
		doc, e := db.ActiveConsentDocument(ctx)
		if e != nil {
			return e
		}
		if doc == nil {
			return domain.ErrConsentUnavailable
		}
		now := s.now().UTC().Truncate(time.Microsecond)
		var participant domain.Participant
		var latest *domain.Consent
		if accepted {
			if documentVersion != doc.Version {
				return domain.ErrConsentVersionStale
			}
			if participant, e = db.EnsureParticipant(ctx, org, user, func() (string, error) { return domain.NewParticipantCode(s.random) }); e != nil {
				return e
			}
			if latest, e = db.LatestConsent(ctx, org, participant.ID); e != nil {
				return e
			}
			if !latest.ValidFor(doc.Version) {
				rec := domain.Consent{DocumentVersion: doc.Version, Accepted: true, At: now, ResponsibleInvestigator: doc.ResponsibleInvestigator}
				if e = db.RecordConsent(ctx, org, user, participant, rec); e != nil {
					return e
				}
				if e = db.Audit(ctx, org, user, "research.consent.accepted", "research_participant", participant.ID, map[string]string{"documentVersion": doc.Version}); e != nil {
					return e
				}
			}
		} else {
			p, e := db.FindParticipant(ctx, org, user, true)
			if e != nil {
				return e
			}
			if p == nil {
				return domain.ErrNoParticipant
			}
			if latest, e = db.LatestConsent(ctx, org, p.ID); e != nil {
				return e
			}
			if latest != nil && latest.Accepted {
				rec := domain.Consent{DocumentVersion: latest.DocumentVersion, Accepted: false, At: now, ResponsibleInvestigator: latest.ResponsibleInvestigator}
				if e = db.RecordConsent(ctx, org, user, *p, rec); e != nil {
					return e
				}
				if e = db.Audit(ctx, org, user, "research.consent.revoked", "research_participant", p.ID, map[string]string{"documentVersion": latest.DocumentVersion}); e != nil {
					return e
				}
			}
		}
		out, e = s.view(ctx, db, user, org)
		return e
	})
	return out, err
}

// SubmissionOutcome es el resultado de una entrega; no incluye respuestas correctas
// para no sesgar el postest.
type SubmissionOutcome struct {
	ID, Moment, VersionID string
	SubmittedAt           time.Time
	Scores                domain.Scores
	Replayed              bool
}

func submissionHash(user, org string, in domain.SubmissionInput) (string, error) {
	raw, err := json.Marshal(struct {
		Kind, User, Org string
		Input           domain.SubmissionInput
	}{"research.submission", user, org, func() domain.SubmissionInput {
		in.Answers = domain.CanonicalAnswers(in.Answers)
		return in
	}()})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// SubmitSurvey registra el PRETEST o POSTTEST. Reglas: miembro activo con
// consentimiento vigente; la versión debe ser la CAP publicada; una sola entrega
// por participante, versión y momento; el servidor calcula los puntajes. Un
// reintento idéntico (mismo id, clientOperationId y contenido) devuelve el mismo
// resultado; el mismo identificador con otro contenido es ErrIdempotencyConflict.
func (s *Service) SubmitSurvey(ctx context.Context, user, org string, in domain.SubmissionInput) (out SubmissionOutcome, err error) {
	now := s.now().UTC()
	if in, err = domain.NormalizeSubmission(in, now); err != nil {
		return out, err
	}
	hash, err := submissionHash(user, org, in)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		if e := s.member(ctx, db, user, org); e != nil {
			return e
		}
		participant, e := db.FindParticipant(ctx, org, user, true)
		if e != nil {
			return e
		}
		if participant == nil {
			return domain.ErrConsentRequired
		}
		previous, e := db.FindSubmission(ctx, org, in.ID, in.ClientOperationID)
		if e != nil {
			return e
		}
		if len(previous) > 0 {
			// El replay se resuelve antes de revisar consentimiento y versión: un
			// reintento tardío debe devolver el resultado original.
			rec := previous[0]
			if len(previous) != 1 || rec.RequestHash != hash || rec.ParticipantID != participant.ID || rec.ID != in.ID {
				return domain.ErrIdempotencyConflict
			}
			out = SubmissionOutcome{ID: rec.ID, Moment: rec.Moment, VersionID: rec.VersionID, SubmittedAt: rec.SubmittedAt, Scores: rec.Scores, Replayed: true}
			return nil
		}
		doc, e := db.ActiveConsentDocument(ctx)
		if e != nil {
			return e
		}
		consent, e := db.LatestConsent(ctx, org, participant.ID)
		if e != nil {
			return e
		}
		if doc == nil || !consent.ValidFor(doc.Version) {
			return domain.ErrConsentRequired
		}
		survey, e := db.PublishedCAP(ctx)
		if e != nil {
			return e
		}
		if survey == nil {
			return domain.ErrSurveyUnavailable
		}
		if in.VersionID != survey.Version.ID {
			return domain.ErrVersionOutdated
		}
		exists, e := db.SubmissionExists(ctx, org, participant.ID, survey.Version.ID, in.Moment)
		if e != nil {
			return e
		}
		if exists {
			return domain.ErrDuplicateSubmission
		}
		scored, scores, e := domain.Score(survey.Questions, in.Answers)
		if e != nil {
			return e
		}
		rec := SubmissionRecord{ID: in.ID, ClientOperationID: in.ClientOperationID, ParticipantID: participant.ID, VersionID: survey.Version.ID,
			Moment: in.Moment, RequestHash: hash, SubmittedAt: now.Truncate(time.Microsecond), ClientCreatedAt: in.ClientCreatedAt, Scores: scores}
		if e = db.InsertSubmission(ctx, org, user, rec, scored); e != nil {
			return e
		}
		out = SubmissionOutcome{ID: rec.ID, Moment: rec.Moment, VersionID: rec.VersionID, SubmittedAt: rec.SubmittedAt, Scores: scores}
		return db.Audit(ctx, org, user, "research.submission."+in.Moment, "survey_submission", in.ID, nil)
	})
	return out, err
}
