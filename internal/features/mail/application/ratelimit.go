package application

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// KeyedLimiter limita eventos por clave (correo o UID) con una ventana deslizante en memoria. Se guarda un hash, nunca la
// dirección. Es por réplica: con varias réplicas el límite efectivo es el límite × réplicas, lo que sigue frenando el abuso.
type KeyedLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
	now    func() time.Time
	sweep  time.Time
}

// NewKeyedLimiter permite como máximo limit eventos por clave dentro de window.
func NewKeyedLimiter(limit int, window time.Duration, now func() time.Time) *KeyedLimiter {
	if now == nil {
		now = time.Now
	}
	return &KeyedLimiter{hits: map[string][]time.Time{}, limit: limit, window: window, now: now, sweep: now()}
}

// Allow registra un intento y devuelve false si la clave superó el límite.
func (l *KeyedLimiter) Allow(key string) bool {
	sum := sha256.Sum256([]byte(key))
	id := hex.EncodeToString(sum[:8])
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.sweep) > l.window {
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > l.window {
				delete(l.hits, k)
			}
		}
		l.sweep = now
	}
	recent := l.hits[id][:0]
	for _, at := range l.hits[id] {
		if now.Sub(at) < l.window {
			recent = append(recent, at)
		}
	}
	if len(recent) >= l.limit {
		l.hits[id] = recent
		return false
	}
	l.hits[id] = append(recent, now)
	return true
}
