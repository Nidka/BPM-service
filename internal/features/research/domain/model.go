// Package domain define las reglas del módulo de investigación CAP
// (conocimientos, actitudes y prácticas): instrumentos versionados e inmutables,
// consentimiento informado, calificación en servidor y participantes
// seudonimizados. No conoce HTTP, SQL ni proveedores externos.
package domain

import (
	"regexp"
	"time"
)

// Dimensiones (secciones) del instrumento CAP.
const (
	SectionKnowledge = "KNOWLEDGE"
	SectionAttitude  = "ATTITUDE"
	SectionPractice  = "PRACTICE"
)

// Tipos de pregunta admitidos; cada tipo pertenece a una única dimensión.
const (
	TypeMultipleChoice = "MULTIPLE_CHOICE"
	TypeLikert         = "LIKERT"
	TypeFrequency      = "FREQUENCY"
)

// Momentos de aplicación.
const (
	MomentPretest  = "PRETEST"
	MomentPosttest = "POSTTEST"
)

// Estados de instrumento, versión y documento de consentimiento.
const (
	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
	StatusArchived  = "ARCHIVED"
)

// Estados del participante.
const (
	ParticipantActive    = "ACTIVE"
	ParticipantWithdrawn = "WITHDRAWN"
)

// Composición exigida al instrumento oficial CAP.
const (
	CAPMultipleChoice = 8
	CAPLikert         = 8
	CAPFrequency      = 10
)

// Límites de contenido.
const (
	MaxQuestions       = 60
	MaxQuestionText    = 500
	MinOptions         = 2
	MaxOptions         = 6
	MaxOptionLength    = 300
	ScaleSize          = 5
	MaxScaleLabel      = 100
	MaxInstrumentName  = 200
	MaxDescription     = 2000
	MaxConsentText     = 20000
	MaxNeedDescription = 2000
	MaxVersionLabel    = 40
)

// Categorías de necesidades detectadas (RF-50).
var NeedCategories = []string{"TRAINING", "PROCESS", "INFRASTRUCTURE", "DOCUMENTATION", "TECHNOLOGY", "SUPPLIERS", "OTHER"}

var (
	uuidPattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	organizationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	codePattern         = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,49}$`)
	versionPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)
	userIDPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// ValidID indica si v es un UUID textual.
func ValidID(v string) bool { return uuidPattern.MatchString(v) }

// ValidOrganization valida el formato del identificador de organización.
func ValidOrganization(v string) bool { return organizationPattern.MatchString(v) }

// ValidUserID valida el formato de un identificador de usuario (UID).
func ValidUserID(v string) bool { return userIDPattern.MatchString(v) }

// ValidMoment indica si v es PRETEST o POSTTEST.
func ValidMoment(v string) bool { return v == MomentPretest || v == MomentPosttest }

// ValidNeedCategory indica si v es una categoría de necesidad conocida.
func ValidNeedCategory(v string) bool {
	for _, c := range NeedCategories {
		if c == v {
			return true
		}
	}
	return false
}

// SectionFor devuelve la dimensión a la que pertenece un tipo de pregunta.
func SectionFor(answerType string) (string, bool) {
	switch answerType {
	case TypeMultipleChoice:
		return SectionKnowledge, true
	case TypeLikert:
		return SectionAttitude, true
	case TypeFrequency:
		return SectionPractice, true
	}
	return "", false
}

// Question es una pregunta publicada o en borrador. CorrectOption solo aplica a
// opción múltiple y nunca debe serializarse hacia el participante.
type Question struct {
	ID            string
	Section       string
	Type          string
	Text          string
	Options       []string
	CorrectOption *int
	Order         int
}

// Instrument es la cabecera global de un instrumento de investigación.
type Instrument struct {
	ID, Code, Name, Description, Status string
	IsCAP                               bool
	CreatedAt                           time.Time
}

// Version es una versión de instrumento. Publicada o archivada es inmutable.
type Version struct {
	ID, InstrumentID, Status string
	Number, RowVersion       int
	PublishedAt              *time.Time
	CreatedAt                time.Time
}

// Draft es la versión en preparación con sus preguntas.
type Draft struct {
	Instrument Instrument
	Version    Version
	Questions  []Question
}

// SurveyVersion es una versión publicada con su instrumento y preguntas.
type SurveyVersion struct {
	Instrument Instrument
	Version    Version
	Questions  []Question
}

// Participant es el participante seudonimizado de una organización.
type Participant struct {
	ID, Code, Status string
}

// ConsentDocument es un documento de consentimiento informado versionado.
type ConsentDocument struct {
	Version, Title, Text, ResponsibleInvestigator, Status string
	PublishedAt                                           time.Time
}

// Consent es el último registro de consentimiento del participante.
type Consent struct {
	DocumentVersion         string
	Accepted                bool
	At                      time.Time
	ResponsibleInvestigator string
}

// ValidFor indica si el consentimiento está aceptado y corresponde al documento vigente.
func (c *Consent) ValidFor(activeVersion string) bool {
	return c != nil && c.Accepted && activeVersion != "" && c.DocumentVersion == activeVersion
}

// Grant es una concesión de acceso de investigación.
type Grant struct {
	UserID, Email, DisplayName string
	GrantedAt                  time.Time
	RevokedAt                  *time.Time
}

// Need es una necesidad detectada en una empresa (RF-50).
type Need struct {
	ID, OrganizationID, OrganizationName, Category, Description string
	InvestigatorID, InvestigatorName                            string
	DetectedAt, CreatedAt                                       time.Time
}

// SubmissionSummary resume una entrega del participante.
type SubmissionSummary struct {
	ID, Moment, VersionID string
	SubmittedAt           time.Time
}

// ValidConsentVersion valida la etiqueta de versión de un documento de consentimiento.
func ValidConsentVersion(v string) bool { return versionPattern.MatchString(v) }
