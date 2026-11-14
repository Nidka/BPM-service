// Package application contiene los casos de uso del módulo de investigación.
// Coordina autorización, idempotencia, auditoría y transacciones; las reglas viven
// en el dominio y el acceso a datos se invierte mediante los puertos de este archivo.
package application

import (
	"context"
	"time"

	"github.com/Nidka/BPM-service/internal/features/research/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// MaxExportRows acota una exportación CSV; un conjunto mayor exige filtrar por empresa.
const MaxExportRows = 20000

// SurveyForm es la versión CAP publicada que ve el participante. Los tipos de
// dominio incluyen CorrectOption; el adaptador HTTP nunca lo serializa.
type SurveyForm struct {
	InstrumentCode, Name, VersionID string
	VersionNumber                   int
	Questions                       []domain.Question
}

// Participation es el estado del participante en una organización.
type Participation struct {
	Participant  *domain.Participant
	Document     *domain.ConsentDocument
	Consent      *domain.Consent
	ConsentValid bool
	Survey       *SurveyForm
	Submissions  []domain.SubmissionSummary
}

// SubmissionRecord es una entrega persistida (sin respuestas).
type SubmissionRecord struct {
	ID, ClientOperationID, ParticipantID, VersionID, Moment, RequestHash string
	SubmittedAt, ClientCreatedAt                                         time.Time
	Scores                                                               domain.Scores
}

// Aggregate resume las entregas de un momento; las medias son nil cuando n=0.
type Aggregate struct {
	N                             int
	Knowledge, Attitude, Practice *float64
}

// PairedAggregate resume la diferencia post - pre de quienes respondieron ambos momentos.
type PairedAggregate struct {
	N                                            int
	KnowledgeDelta, AttitudeDelta, PracticeDelta *float64
}

// OrganizationResult son los resultados consolidados de una empresa (RF-26).
type OrganizationResult struct {
	OrganizationID, Name string
	Participants         int
	Pretest, Posttest    Aggregate
	Paired               PairedAggregate
}

// ExportRow es una fila anonimizada: solo código seudónimo, empresa, momento,
// respuestas y puntajes.
type ExportRow struct {
	ParticipantCode, OrganizationID, Moment string
	SubmittedAt                             time.Time
	Answers                                 []int
	Scores                                  domain.Scores
}

// ExportData es la exportación de una versión del instrumento.
type ExportData struct {
	VersionNumber int
	Questions     []domain.Question
	Rows          []ExportRow
}

// OrganizationRef identifica una empresa participante.
type OrganizationRef struct{ ID, Name string }

// InstrumentSummary es una fila del listado de instrumentos.
type InstrumentSummary struct {
	Instrument       domain.Instrument
	PublishedVersion *int
	HasDraft         bool
}

// InstrumentDetail incluye todas las versiones del instrumento.
type InstrumentDetail struct {
	Instrument domain.Instrument
	Versions   []domain.Version
}

// Store reúne las operaciones disponibles dentro de una transacción.
type Store interface {
	// Autorización: el acceso de investigación es una concesión de plataforma,
	// independiente de los roles de empresa.
	IsResearcher(ctx context.Context, user string) (bool, error)
	IsSuperAdmin(ctx context.Context, user string) (bool, error)
	// IsMember indica si el usuario tiene membresía ACTIVE en una organización ACTIVE.
	IsMember(ctx context.Context, user, org string) (bool, error)
	// Audit registra actor, acción y entidad. org vacío guarda NULL (acceso de plataforma).
	Audit(ctx context.Context, org, user, action, entityType, entityID string, metadata map[string]string) error

	UserByEmail(ctx context.Context, email string) (domain.Grant, error)
	SaveGrant(ctx context.Context, by, userID string, at time.Time) (domain.Grant, error)
	RevokeGrant(ctx context.Context, by, userID string, at time.Time) error
	Grants(ctx context.Context, w pagination.Window) ([]domain.Grant, error)

	ActiveConsentDocument(ctx context.Context) (*domain.ConsentDocument, error)
	PublishConsentDocument(ctx context.Context, user string, in domain.ConsentDocumentInput, at time.Time) (domain.ConsentDocument, error)
	ConsentDocuments(ctx context.Context, w pagination.Window) ([]domain.ConsentDocument, error)

	// EnsureParticipant devuelve el participante del usuario o lo crea con un código
	// generado por newCode (reintenta si el código ya existe).
	EnsureParticipant(ctx context.Context, org, user string, newCode func() (string, error)) (domain.Participant, error)
	// FindParticipant devuelve nil si el usuario aún no se inscribió; lock lo bloquea.
	FindParticipant(ctx context.Context, org, user string, lock bool) (*domain.Participant, error)
	LatestConsent(ctx context.Context, org, participantID string) (*domain.Consent, error)
	// RecordConsent agrega un registro inmutable y actualiza el estado del participante.
	RecordConsent(ctx context.Context, org, user string, participant domain.Participant, rec domain.Consent) error

	CreateInstrument(ctx context.Context, user string, in domain.InstrumentInput, questions []domain.Question) (domain.Draft, error)
	Instruments(ctx context.Context, w pagination.Window) ([]InstrumentSummary, error)
	InstrumentDetail(ctx context.Context, id string) (InstrumentDetail, error)
	// LockInstrument bloquea el instrumento o devuelve ErrInstrumentNotFound.
	LockInstrument(ctx context.Context, id string) (domain.Instrument, error)
	// Draft devuelve ErrDraftNotFound si no hay borrador.
	Draft(ctx context.Context, instrument domain.Instrument) (domain.Draft, error)
	// CreateDraft copia la última versión publicada en una nueva versión DRAFT.
	CreateDraft(ctx context.Context, user string, instrument domain.Instrument) (domain.Draft, error)
	ReplaceDraft(ctx context.Context, user string, draft domain.Draft, rowVersion int, questions []domain.Question) (domain.Draft, error)
	PublishDraft(ctx context.Context, user string, draft domain.Draft, at time.Time) (domain.Version, error)
	// PublishedCAP devuelve la versión PUBLISHED del instrumento CAP o nil.
	PublishedCAP(ctx context.Context) (*domain.SurveyVersion, error)

	FindSubmission(ctx context.Context, org, id, operationID string) ([]SubmissionRecord, error)
	SubmissionExists(ctx context.Context, org, participantID, versionID, moment string) (bool, error)
	InsertSubmission(ctx context.Context, org, user string, rec SubmissionRecord, answers []domain.ScoredAnswer) error
	Submissions(ctx context.Context, org, participantID string) ([]domain.SubmissionSummary, error)

	// CAPVersion devuelve la versión indicada solo si pertenece al instrumento CAP.
	CAPVersion(ctx context.Context, versionID string) (*domain.SurveyVersion, error)
	OrganizationResults(ctx context.Context, versionID string, w pagination.Window) ([]OrganizationResult, error)
	ExportRows(ctx context.Context, versionID, org string, limit int) ([]ExportRow, error)
	Organizations(ctx context.Context, w pagination.Window) ([]OrganizationRef, error)
	OrganizationExists(ctx context.Context, id string) (bool, error)

	AddNeed(ctx context.Context, user string, in domain.NeedInput) (domain.Need, error)
	Needs(ctx context.Context, org string, w pagination.Window) ([]domain.Need, error)
}

// UnitOfWork ejecuta fn en una transacción; si fn devuelve error se revierte.
type UnitOfWork interface {
	Within(ctx context.Context, fn func(Store) error) error
}
