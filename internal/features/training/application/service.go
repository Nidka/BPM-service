package application

import (
	"context"
	"crypto/rand"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/core/contracts"
	"github.com/Nidka/BPM-service/internal/features/training/domain"
)

// Service reúne los casos de uso de la Academia BPM.
type Service struct {
	work   UnitOfWork
	videos contracts.VideoStorage
	policy domain.VideoPolicy
	now    func() time.Time
	random io.Reader
	// rnd es la fuente de azar de los sorteos de evaluación (crypto/rand salvo en pruebas).
	rnd domain.Rand
}

// New crea el servicio. videos puede ser nil cuando R2 no está habilitado; los
// casos de uso de video responden entonces ErrUnavailable.
func New(work UnitOfWork, videos contracts.VideoStorage, policy domain.VideoPolicy) *Service {
	return &Service{work: work, videos: videos, policy: policy, now: time.Now, rnd: cryptoRand{}}
}

// actor autoriza al usuario dentro de la organización antes de cualquier lectura de negocio.
func (s *Service) actor(ctx context.Context, db Store, user, org string, allowed func(domain.Actor) bool) (domain.Actor, error) {
	a, err := db.Actor(ctx, user, org)
	if err != nil {
		return a, err
	}
	if !allowed(a) {
		return a, domain.ErrDenied
	}
	return a, nil
}

func learner(a domain.Actor) bool   { return a.CanLearn() }
func persister(a domain.Actor) bool { return a.CanPersist() }
func author(a domain.Actor) bool    { return a.CanAuthor() }

// courseKey valida y normaliza los identificadores de la ruta; los inválidos son 404.
func courseKey(org, courseID string) (string, error) {
	courseID = strings.ToLower(courseID)
	if !domain.ValidOrganization(org) || !domain.ValidID(courseID) {
		return "", domain.ErrCourseNotFound
	}
	return courseID, nil
}

// cryptoRand sortea con crypto/rand: el orden de las alternativas no debe ser predecible.
type cryptoRand struct{}

// Intn devuelve un entero uniforme en [0, n) sin sesgo de módulo.
func (cryptoRand) Intn(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// Sin entropía del sistema no hay forma segura de sortear: se detiene la operación.
		panic("training: crypto/rand no disponible")
	}
	return int(v.Int64())
}
