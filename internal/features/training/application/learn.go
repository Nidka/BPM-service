package application

import (
	"context"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// Course devuelve la versión publicada del curso para aprender. Las preguntas no
// incluyen respuesta correcta ni explicación.
func (s *Service) Course(ctx context.Context, user, org, courseID string) (out domain.CourseView, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := s.actor(ctx, db, user, org, learner)
		if e != nil {
			return e
		}
		progressUser := ""
		if a.CanPersist() {
			progressUser = user
		}
		out, e = db.CourseView(ctx, org, progressUser, courseID)
		return e
	})
	return out, err
}

// VideoAccess es una URL firmada de lectura para un video de lección.
type VideoAccess struct {
	URL       string
	MIMEType  string
	ExpiresAt time.Time
}

// LessonVideo autoriza y firma un GET de 15 minutos. La URL no se registra.
func (s *Service) LessonVideo(ctx context.Context, user, org, courseID, lessonID string) (VideoAccess, error) {
	courseID, err := courseKey(org, courseID)
	if err != nil {
		return VideoAccess{}, err
	}
	if lessonID = strings.ToLower(lessonID); !domain.ValidID(lessonID) {
		return VideoAccess{}, domain.ErrVideoNotFound
	}
	var file domain.VideoFile
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, learner); e != nil {
			return e
		}
		version, e := db.PublishedVersion(ctx, org, courseID)
		if e != nil {
			return e
		}
		file, e = db.LessonVideo(ctx, org, version.ID, lessonID)
		return e
	})
	if err != nil {
		return VideoAccess{}, err
	}
	if s.videos == nil {
		return VideoAccess{}, domain.ErrUnavailable
	}
	ticket, err := s.videos.PresignVideoDownload(ctx, file.ObjectKey)
	if err != nil {
		return VideoAccess{}, domain.ErrUnavailable
	}
	return VideoAccess{URL: ticket.URL, MIMEType: file.MIMEType, ExpiresAt: ticket.ExpiresAt}, nil
}

// SaveProgress registra la última lección vista (retomar, RF-11). Es idempotente:
// repetir el mismo valor produce el mismo resultado y COMPLETED nunca retrocede.
func (s *Service) SaveProgress(ctx context.Context, user, org, courseID, lessonID string) (out domain.Progress, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	if lessonID = strings.ToLower(lessonID); !domain.ValidID(lessonID) {
		return out, &domain.ValidationError{Fields: map[string]string{"lastLessonId": "Elige una lección válida."}}
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
		if e = db.LessonInVersion(ctx, org, version.ID, lessonID); e != nil {
			return e
		}
		current, e := db.Progress(ctx, org, user, courseID)
		if e != nil {
			return e
		}
		out = current.WithLesson(lessonID)
		return db.SaveProgress(ctx, org, user, courseID, out)
	})
	return out, err
}

// StudyAnswer da retroalimentación inmediata del modo estudio. No califica, no
// persiste nada y no otorga XP ni constancia.
func (s *Service) StudyAnswer(ctx context.Context, user, org, courseID, questionID string, selected int) (out domain.StudyFeedback, err error) {
	if courseID, err = courseKey(org, courseID); err != nil {
		return out, err
	}
	if questionID = strings.ToLower(questionID); !domain.ValidID(questionID) {
		return out, &domain.ValidationError{Fields: map[string]string{"questionId": "Elige una pregunta válida."}}
	}
	err = s.work.Within(ctx, func(db Store) error {
		if _, e := s.actor(ctx, db, user, org, learner); e != nil {
			return e
		}
		version, e := db.PublishedVersion(ctx, org, courseID)
		if e != nil {
			return e
		}
		question, e := db.Question(ctx, org, version.ID, questionID)
		if e != nil {
			return e
		}
		out, e = domain.Study(question, selected)
		return e
	})
	return out, err
}
