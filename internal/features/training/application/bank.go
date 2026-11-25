package application

import (
	"context"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/catalog"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// EvaluationInput es la solicitud de iniciar una evaluación sorteada. ID es el que tendrá el intento.
type EvaluationInput struct{ ID, AttemptType string }

// Evaluation es la evaluación sorteada que ve quien aprende: preguntas con las alternativas ya barajadas y sin respuesta.
type Evaluation struct {
	ID, VersionID, AttemptType string
	BankVersion, AttemptNumber int
	PassingScore               int
	Questions                  []domain.PublicQuestion
	CreatedAt                  time.Time
	Replayed                   bool
}

// StartEvaluation sortea la evaluación de un curso con banco oficial: 5 ítems (la selección recomendada la
// primera vez y, después, una rotación balanceada por Bloom) con las alternativas en orden aleatorio. El sorteo
// queda guardado con el id del intento para que el servidor califique y repetir la solicitud devuelva el mismo.
// Las respuestas correctas nunca viajan al cliente.
func (s *Service) StartEvaluation(ctx context.Context, user, org, courseID string, in EvaluationInput) (out Evaluation, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	in.ID, in.AttemptType = strings.ToLower(in.ID), strings.ToUpper(in.AttemptType)
	fields := map[string]string{}
	if !domain.ValidID(in.ID) {
		fields["id"] = "Debe ser un identificador UUID válido."
	}
	if in.AttemptType != domain.AttemptDiagnostic && in.AttemptType != domain.AttemptFinal {
		fields["attemptType"] = "El tipo de evaluación debe ser DIAGNOSTIC o FINAL."
	}
	if len(fields) > 0 {
		return out, &domain.ValidationError{Fields: fields}
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, persister); e != nil {
			return e
		}
		if e := db.LockLearner(ctx, org, user, courseID); e != nil {
			return e
		}
		version, e := db.PublishedVersion(ctx, org, courseID)
		if e != nil {
			return e
		}
		questions, e := db.Questions(ctx, org, version.ID)
		if e != nil {
			return e
		}
		pool := domain.BankPool(questions)
		if len(pool) == 0 {
			return domain.ErrNoBank
		}
		draw, found, e := db.FindDraw(ctx, org, in.ID)
		if e != nil {
			return e
		}
		replayed := found
		if found {
			if draw.UserID != user || draw.VersionID != version.ID || draw.AttemptType != in.AttemptType {
				return domain.ErrIdempotencyConflict
			}
		} else {
			history, e := db.ItemHistory(ctx, org, user, courseID)
			if e != nil {
				return e
			}
			bank, _ := catalog.MustLoad()
			draw = domain.Draw{ID: in.ID, UserID: user, VersionID: version.ID, AttemptType: in.AttemptType, BankVersion: bankVersion(pool),
				AttemptNumber: attemptNumber(history), CreatedAt: s.now().UTC().Truncate(time.Microsecond)}
			draw.Items = domain.DrawEvaluation(pool, bank.Recommended(catalog.ModuleOf(pool[0].ItemCode)), history, s.rnd)
			if e = db.InsertDraw(ctx, org, draw); e != nil {
				return e
			}
			if e = db.Audit(ctx, org, user, "training.evaluation.drawn", "training_evaluation", in.ID); e != nil {
				return e
			}
		}
		out = presentDraw(draw, questions, version.PassingScore)
		out.Replayed = replayed
		return nil
	})
	return out, err
}

// attemptNumber es el número de evaluación de la persona: 1 la primera vez (sin historial).
func attemptNumber(history []domain.ItemUse) int {
	most := 0
	for _, h := range history {
		if h.Uses > most {
			most = h.Uses
		}
	}
	return most + 1
}

func bankVersion(pool []domain.Question) int {
	version := 0
	for _, q := range pool {
		if q.BankVersion > version {
			version = q.BankVersion
		}
	}
	return version
}

// presentDraw arma las preguntas del sorteo para quien aprende, con las alternativas en el orden sorteado.
func presentDraw(draw domain.Draw, questions []domain.Question, passingScore int) Evaluation {
	byID := make(map[string]domain.Question, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	out := Evaluation{ID: draw.ID, VersionID: draw.VersionID, AttemptType: draw.AttemptType, BankVersion: draw.BankVersion,
		AttemptNumber: draw.AttemptNumber, PassingScore: passingScore, CreatedAt: draw.CreatedAt, Questions: make([]domain.PublicQuestion, 0, len(draw.Items))}
	for i, item := range draw.Items {
		q := byID[item.QuestionID]
		out.Questions = append(out.Questions, domain.PublicQuestion{ID: q.ID, LessonID: q.LessonID, Text: q.Text,
			Options: domain.ShownOptions(q, item.OptionOrder), DisplayOrder: i + 1, BankVersion: q.BankVersion})
	}
	return out
}

// BankItem es un ítem del banco tal como lo ve la administración (con clave y justificación).
type BankItem struct {
	Question domain.Question
	// Recommended indica que forma parte de la evaluación recomendada del primer intento.
	Recommended bool
	// LessonTitle es la unidad en que se practica el ítem.
	LessonTitle string
}

// BankView es el banco de un curso para administración, solo lectura.
type BankView struct {
	Course  domain.Course
	Version domain.Version
	Module  string
	Items   []BankItem
	Sources []catalog.Source
}

// Bank devuelve los ítems del banco de un curso con su clave y justificación (solo ADMIN y SUPER_ADMIN).
func (s *Service) Bank(ctx context.Context, user, org, courseID string) (out BankView, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, author); e != nil {
			return e
		}
		view, e := db.CourseView(ctx, org, "", courseID)
		if e != nil {
			return e
		}
		questions, e := db.Questions(ctx, org, view.Version.ID)
		if e != nil {
			return e
		}
		pool := domain.BankPool(questions)
		if len(pool) == 0 {
			return domain.ErrNoBank
		}
		bank, _ := catalog.MustLoad()
		module := catalog.ModuleOf(pool[0].ItemCode)
		recommended := map[string]bool{}
		for _, code := range bank.Recommended(module) {
			recommended[code] = true
		}
		titles := map[string]string{}
		for _, l := range view.Lessons {
			titles[l.ID] = l.Title
		}
		out = BankView{Course: view.Course, Version: view.Version, Module: module, Sources: bank.Sources}
		for _, q := range pool {
			out.Items = append(out.Items, BankItem{Question: q, Recommended: recommended[q.ItemCode], LessonTitle: titles[q.LessonID]})
		}
		return nil
	})
	return out, err
}

// SourceLabels resuelve los códigos de fuente (P01) a su etiqueta corta, título y enlace del banco.
func SourceLabels(codes []string) []catalog.Source {
	bank, _ := catalog.MustLoad()
	out := make([]catalog.Source, 0, len(codes))
	for _, code := range codes {
		if source, ok := bank.Source(code); ok {
			out = append(out, source)
		}
	}
	return out
}

// keepBankItems impide editar los ítems oficiales desde el borrador: una pregunta con el mismo id que un
// ítem del banco conserva su texto, alternativas, clave, explicación y metadatos; solo se puede quitar o
// reubicar. Las preguntas nuevas del cliente nunca llevan metadatos de banco.
func keepBankItems(previous, next []domain.Question) []domain.Question {
	official := make(map[string]domain.Question, len(previous))
	for _, q := range previous {
		if q.FromBank() {
			official[q.ID] = q
		}
	}
	out := make([]domain.Question, len(next))
	for i, q := range next {
		if base, ok := official[strings.ToLower(q.ID)]; ok {
			base.LessonID, base.DisplayOrder = q.LessonID, q.DisplayOrder
			q = base
		} else {
			q.ItemCode, q.BankVersion, q.Bloom, q.Sources = "", 0, "", nil
		}
		out[i] = q
	}
	return out
}
