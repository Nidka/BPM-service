package researchhttp

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Nidka/BPM-service/internal/features/research/application"
	"github.com/Nidka/BPM-service/internal/features/research/domain"
)

// ---- Entrada (tipos distintos de las entidades de dominio) ----

type consentBody struct {
	DocumentVersion string `json:"documentVersion"`
	Accepted        *bool  `json:"accepted"`
}

type answerBody struct {
	QuestionID string `json:"questionId"`
	Value      *int   `json:"value"`
}

type submissionBody struct {
	ID                  string       `json:"id"`
	ClientOperationID   string       `json:"clientOperationId"`
	InstrumentVersionID string       `json:"instrumentVersionId"`
	Moment              string       `json:"moment"`
	ClientCreatedAt     time.Time    `json:"clientCreatedAt"`
	Answers             []answerBody `json:"answers"`
}

type grantBody struct {
	Email string `json:"email"`
}

type consentDocumentBody struct {
	Version                 string `json:"version"`
	Title                   string `json:"title"`
	Text                    string `json:"text"`
	ResponsibleInvestigator string `json:"responsibleInvestigator"`
}

type questionBody struct {
	Type          string   `json:"type"`
	Text          string   `json:"text"`
	Options       []string `json:"options"`
	CorrectOption *int     `json:"correctOption"`
}

func questionInputs(in []questionBody) []domain.QuestionInput {
	out := make([]domain.QuestionInput, 0, len(in))
	for _, q := range in {
		out = append(out, domain.QuestionInput{Type: q.Type, Text: q.Text, Options: q.Options, CorrectOption: q.CorrectOption})
	}
	return out
}

type instrumentBody struct {
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	IsCAP       bool           `json:"isCap"`
	Questions   []questionBody `json:"questions"`
}

// importBody es el formato de docs/research/cap-instrument.json: añade metadatos
// informativos (source, generalData) que se aceptan pero no se persisten.
type importBody struct {
	instrumentBody
	Source      string          `json:"source"`
	GeneralData json.RawMessage `json:"generalData"`
}

// UnmarshalJSON permite incrustar instrumentBody con DisallowUnknownFields.
func (b *importBody) UnmarshalJSON(raw []byte) error {
	var flat struct {
		Code        string          `json:"code"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		IsCAP       bool            `json:"isCap"`
		Questions   []questionBody  `json:"questions"`
		Source      string          `json:"source"`
		GeneralData json.RawMessage `json:"generalData"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&flat); err != nil {
		return err
	}
	b.instrumentBody = instrumentBody{Code: flat.Code, Name: flat.Name, Description: flat.Description, IsCAP: flat.IsCAP, Questions: flat.Questions}
	b.Source, b.GeneralData = flat.Source, flat.GeneralData
	return nil
}

// rowVersion acepta el valor como texto ("3") o número (3).
type rowVersion int

func (v *rowVersion) UnmarshalJSON(raw []byte) error {
	n, err := strconv.Atoi(string(bytes.Trim(bytes.TrimSpace(raw), `"`)))
	if err != nil {
		return err
	}
	*v = rowVersion(n)
	return nil
}

type draftBody struct {
	RowVersion *rowVersion    `json:"rowVersion"`
	Questions  []questionBody `json:"questions"`
}

type needBody struct {
	OrganizationID string     `json:"organizationId"`
	Category       string     `json:"category"`
	Description    string     `json:"description"`
	DetectedAt     *time.Time `json:"detectedAt"`
}

// ---- Salida ----

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func tsp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := ts(*t)
	return &s
}

// participantQuestion es la pregunta que ve el participante: no tiene campo de respuesta correcta.
type participantQuestion struct {
	ID        string   `json:"id"`
	Order     int      `json:"order"`
	Dimension string   `json:"dimension"`
	Type      string   `json:"type"`
	Text      string   `json:"text"`
	Options   []string `json:"options"`
}

func scoresDTO(s domain.Scores) map[string]any {
	return map[string]any{"knowledgeCorrect": s.KnowledgeCorrect, "knowledgeTotal": s.KnowledgeTotal,
		"knowledgePercent": s.Knowledge, "attitudeMean": s.Attitude, "practiceMean": s.Practice}
}

func participationDTO(p application.Participation) map[string]any {
	out := map[string]any{"participant": nil, "consentDocument": nil, "consent": nil, "consentValid": p.ConsentValid, "survey": nil, "submissions": []any{}}
	if p.Participant != nil {
		out["participant"] = map[string]any{"code": p.Participant.Code, "status": p.Participant.Status}
	}
	if d := p.Document; d != nil {
		out["consentDocument"] = map[string]any{"version": d.Version, "title": d.Title, "text": d.Text,
			"responsibleInvestigator": d.ResponsibleInvestigator, "publishedAt": ts(d.PublishedAt)}
	}
	if c := p.Consent; c != nil {
		out["consent"] = map[string]any{"documentVersion": c.DocumentVersion, "accepted": c.Accepted, "recordedAt": ts(c.At), "responsibleInvestigator": c.ResponsibleInvestigator}
	}
	if sv := p.Survey; sv != nil {
		qs := make([]participantQuestion, 0, len(sv.Questions))
		for _, q := range sv.Questions {
			qs = append(qs, participantQuestion{ID: q.ID, Order: q.Order, Dimension: q.Section, Type: q.Type, Text: q.Text, Options: q.Options})
		}
		out["survey"] = map[string]any{"instrumentCode": sv.InstrumentCode, "name": sv.Name, "instrumentVersionId": sv.VersionID, "versionNumber": sv.VersionNumber, "questions": qs}
	}
	subs := make([]map[string]any, 0, len(p.Submissions))
	for _, s := range p.Submissions {
		subs = append(subs, map[string]any{"moment": s.Moment, "instrumentVersionId": s.VersionID, "submittedAt": ts(s.SubmittedAt)})
	}
	out["submissions"] = subs
	return out
}

func grantDTO(g domain.Grant) map[string]any {
	return map[string]any{"userId": g.UserID, "email": g.Email, "displayName": g.DisplayName, "grantedAt": ts(g.GrantedAt), "revokedAt": tsp(g.RevokedAt), "active": g.RevokedAt == nil}
}

func consentDocumentDTO(d domain.ConsentDocument) map[string]any {
	return map[string]any{"version": d.Version, "title": d.Title, "text": d.Text, "responsibleInvestigator": d.ResponsibleInvestigator, "status": d.Status, "publishedAt": ts(d.PublishedAt)}
}

func instrumentDTO(i domain.Instrument) map[string]any {
	return map[string]any{"id": i.ID, "code": i.Code, "name": i.Name, "description": i.Description, "isCap": i.IsCAP, "status": i.Status, "createdAt": ts(i.CreatedAt)}
}

func versionDTO(v domain.Version) map[string]any {
	return map[string]any{"id": v.ID, "versionNumber": v.Number, "status": v.Status, "rowVersion": v.RowVersion, "publishedAt": tsp(v.PublishedAt)}
}

// authorQuestion incluye la respuesta correcta; solo se usa en rutas de investigador.
type authorQuestion struct {
	ID            string   `json:"id"`
	Order         int      `json:"order"`
	Dimension     string   `json:"dimension"`
	Type          string   `json:"type"`
	Text          string   `json:"text"`
	Options       []string `json:"options"`
	CorrectOption *int     `json:"correctOption"`
}

func draftDTO(d domain.Draft) map[string]any {
	qs := make([]authorQuestion, 0, len(d.Questions))
	for _, q := range d.Questions {
		qs = append(qs, authorQuestion{ID: q.ID, Order: q.Order, Dimension: q.Section, Type: q.Type, Text: q.Text, Options: q.Options, CorrectOption: q.CorrectOption})
	}
	return map[string]any{"instrument": instrumentDTO(d.Instrument), "version": versionDTO(d.Version), "questions": qs}
}

func aggregateDTO(a application.Aggregate) map[string]any {
	return map[string]any{"n": a.N, "knowledgeMean": a.Knowledge, "attitudeMean": a.Attitude, "practiceMean": a.Practice}
}

func resultDTO(r application.OrganizationResult) map[string]any {
	return map[string]any{"organizationId": r.OrganizationID, "organizationName": r.Name, "participants": r.Participants,
		"pretest": aggregateDTO(r.Pretest), "posttest": aggregateDTO(r.Posttest),
		"paired": map[string]any{"n": r.Paired.N, "knowledgeDelta": r.Paired.KnowledgeDelta, "attitudeDelta": r.Paired.AttitudeDelta, "practiceDelta": r.Paired.PracticeDelta}}
}

func needDTO(n domain.Need) map[string]any {
	return map[string]any{"id": n.ID, "organizationId": n.OrganizationID, "organizationName": n.OrganizationName, "category": n.Category,
		"description": n.Description, "detectedAt": ts(n.DetectedAt), "investigatorId": n.InvestigatorID, "investigatorName": n.InvestigatorName, "createdAt": ts(n.CreatedAt)}
}
