package domain

import (
	"crypto/rand"
	"encoding/base32"
	"io"
	"regexp"
	"time"
)

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)
var codePattern = regexp.MustCompile(`^[A-Z2-7]{24}$`)

// ValidVerificationCode valida el formato del código público (24 caracteres base32).
func ValidVerificationCode(v string) bool { return codePattern.MatchString(v) }

// NewVerificationCode genera un código aleatorio de 120 bits con crypto/rand.
func NewVerificationCode(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	raw := make([]byte, 15)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return codeEncoding.EncodeToString(raw), nil
}

// Settings es la configuración de reportes de una empresa.
type Settings struct {
	WeeklyEmailEnabled  bool
	ComplianceThreshold float64
}

// DefaultSettings son los valores cuando la empresa no ha configurado nada.
func DefaultSettings() Settings {
	return Settings{WeeklyEmailEnabled: true, ComplianceThreshold: DefaultThreshold}
}

// ValidateSettings valida el umbral (1 a 100, dos decimales como máximo).
func ValidateSettings(s Settings) error {
	if s.ComplianceThreshold < MinThreshold || s.ComplianceThreshold > MaxThreshold || Round2(s.ComplianceThreshold) != s.ComplianceThreshold {
		return &ValidationError{Fields: map[string]string{"complianceThreshold": "Usa un umbral entre 1 y 100 con hasta dos decimales."}}
	}
	return nil
}

// Certificate es una constancia de cumplimiento emitida; es inmutable.
type Certificate struct {
	ID                string
	PeriodStart       time.Time
	PeriodEnd         time.Time
	AverageCompliance float64
	Threshold         float64
	Runs              int
	VerificationCode  string
	IssuedAt          time.Time
}

// PublicCertificate es la vista verificable por código; no revela identificadores internos.
type PublicCertificate struct {
	OrganizationName  string
	PeriodStart       time.Time
	PeriodEnd         time.Time
	AverageCompliance float64
	Threshold         float64
	IssuedAt          time.Time
	VerificationCode  string
}

// CheckEligibility aplica la regla de emisión: debe haber ejecuciones y el promedio alcanzar el umbral.
func CheckEligibility(average *float64, threshold float64) error {
	if average == nil {
		return ErrNoData
	}
	if *average < threshold {
		return &ThresholdError{Average: *average, Threshold: threshold}
	}
	return nil
}

// CertificatePeriod valida el período de una constancia: debe estar terminado (anterior al día
// de hoy en Lima) y no superar el máximo.
func CertificatePeriod(from, to string, now time.Time) (Period, error) {
	p, err := ParsePeriod(from, to, MaxCertificateDays)
	if err != nil {
		return p, err
	}
	local := now.In(Lima)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	if !p.To.Before(today) {
		return Period{}, &ValidationError{Fields: map[string]string{"to": "El período debe haber terminado antes de hoy."}}
	}
	return p, nil
}
