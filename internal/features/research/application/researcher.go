package application

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Nidka/BPM-service/internal/features/research/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
)

// ---- Concesiones de acceso (solo SUPER_ADMIN) ----

func (s *Service) superAdmin(ctx context.Context, db Store, user string) error {
	ok, err := db.IsSuperAdmin(ctx, user)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrDenied
	}
	return nil
}

// GrantAccess concede (o reactiva) el acceso de investigación a un usuario activo
// identificado por su correo. Solo SUPER_ADMIN; es idempotente.
func (s *Service) GrantAccess(ctx context.Context, actor, email string) (g domain.Grant, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) < 3 || len(email) > 320 || strings.Count(email, "@") != 1 || strings.ContainsAny(email, " \t\r\n") {
		return g, &domain.ValidationError{Fields: map[string]string{"email": "Ingresa un correo válido."}}
	}
	err = s.work.Within(ctx, func(db Store) error {
		if e := s.superAdmin(ctx, db, actor); e != nil {
			return e
		}
		target, e := db.UserByEmail(ctx, email)
		if e != nil {
			return e
		}
		if g, e = db.SaveGrant(ctx, actor, target.UserID, s.now().UTC()); e != nil {
			return e
		}
		return db.Audit(ctx, "", actor, "research.access.granted", "research_access_grant", g.UserID, nil)
	})
	return g, err
}

// RevokeAccess revoca el acceso de investigación; el historial se conserva.
func (s *Service) RevokeAccess(ctx context.Context, actor, userID string) error {
	if !domain.ValidUserID(userID) {
		return domain.ErrGrantNotFound
	}
	return s.work.Within(ctx, func(db Store) error {
		if e := s.superAdmin(ctx, db, actor); e != nil {
			return e
		}
		if e := db.RevokeGrant(ctx, actor, userID, s.now().UTC()); e != nil {
			return e
		}
		return db.Audit(ctx, "", actor, "research.access.revoked", "research_access_grant", userID, nil)
	})
}

// ListGrants lista las concesiones (vigentes y revocadas) por identificador de usuario.
func (s *Service) ListGrants(ctx context.Context, actor string, p pagination.Request) (out pagination.Page[domain.Grant], err error) {
	scope := []string{actor, "research-grants"}
	w, err := pagination.Open(p, scope...)
	if err != nil {
		return out, err
	}
	err = s.work.Within(ctx, func(db Store) error {
		if e := s.superAdmin(ctx, db, actor); e != nil {
			return e
		}
		items, e := db.Grants(ctx, w)
		if e != nil {
			return e
		}
		out = pagination.Close(items, w, func(g domain.Grant) pagination.Position { return pagination.Position{ID: g.UserID} }, scope...)
		return db.Audit(ctx, "", actor, "research.access.listed", "research_access_grant", "", nil)
	})
	return out, err
}

// ---- Documentos de consentimiento ----

// PublishConsentDocument publica un documento y archiva el vigente. Los
// participantes deberán aceptar la nueva versión para seguir respondiendo.
func (s *Service) PublishConsentDocument(ctx context.Context, user string, in domain.ConsentDocumentInput) (doc domain.ConsentDocument, err error) {
	if in, err = domain.NormalizeConsentDocument(in); err != nil {
		return doc, err
	}
	err = s.asResearcher(ctx, user, "", "research.consent_document.published", "research_consent_document", in.Version, nil, func(db Store) error {
		var e error
		doc, e = db.PublishConsentDocument(ctx, user, in, s.now().UTC())
		return e
	})
	return doc, err
}

// ListConsentDocuments lista los documentos por versión.
func (s *Service) ListConsentDocuments(ctx context.Context, user string, p pagination.Request) (out pagination.Page[domain.ConsentDocument], err error) {
	scope := []string{user, "research-consent-documents"}
	w, err := pagination.Open(p, scope...)
	if err != nil {
		return out, err
	}
	err = s.asResearcher(ctx, user, "", "research.consent_document.listed", "research_consent_document", "", nil, func(db Store) error {
		items, e := db.ConsentDocuments(ctx, w)
		out = pagination.Close(items, w, func(d domain.ConsentDocument) pagination.Position { return pagination.Position{ID: d.Version} }, scope...)
		return e
	})
	return out, err
}

// ---- Instrumentos ----

// CreateInstrument crea un instrumento con su versión 1 en borrador. Se usa tanto
// para la creación manual como para importar un JSON (p. ej. el cuestionario CAP).
// Las preguntas pueden no tener respuesta correcta aún; publicar la exige.
func (s *Service) CreateInstrument(ctx context.Context, user string, header domain.InstrumentInput, questions []domain.QuestionInput) (draft domain.Draft, err error) {
	if header, err = domain.NormalizeInstrument(header); err != nil {
		return draft, err
	}
	qs, err := domain.NormalizeQuestions(questions)
	if err != nil {
		return draft, err
	}
	err = s.asResearcher(ctx, user, "", "research.instrument.created", "survey_instrument", header.Code, map[string]string{"isCap": boolText(header.IsCAP)}, func(db Store) error {
		var e error
		draft, e = db.CreateInstrument(ctx, user, header, qs)
		return e
	})
	return draft, err
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// ListInstruments lista los instrumentos por código.
func (s *Service) ListInstruments(ctx context.Context, user string, p pagination.Request) (out pagination.Page[InstrumentSummary], err error) {
	scope := []string{user, "research-instruments"}
	w, err := pagination.Open(p, scope...)
	if err != nil {
		return out, err
	}
	if err = validUUIDCursor(w); err != nil {
		return out, err
	}
	err = s.asResearcher(ctx, user, "", "research.instrument.listed", "survey_instrument", "", nil, func(db Store) error {
		items, e := db.Instruments(ctx, w)
		out = pagination.Close(items, w, func(i InstrumentSummary) pagination.Position {
			return pagination.Position{Key: i.Instrument.Code, ID: i.Instrument.ID}
		}, scope...)
		return e
	})
	return out, err
}

// GetInstrument devuelve el instrumento y sus versiones.
func (s *Service) GetInstrument(ctx context.Context, user, id string) (d InstrumentDetail, err error) {
	if id = strings.ToLower(id); !domain.ValidID(id) {
		return d, domain.ErrInstrumentNotFound
	}
	err = s.asResearcher(ctx, user, "", "research.instrument.read", "survey_instrument", id, nil, func(db Store) error {
		var e error
		d, e = db.InstrumentDetail(ctx, id)
		return e
	})
	return d, err
}

// GetDraft devuelve el borrador editable, con respuestas correctas.
func (s *Service) GetDraft(ctx context.Context, user, id string) (d domain.Draft, err error) {
	if id = strings.ToLower(id); !domain.ValidID(id) {
		return d, domain.ErrInstrumentNotFound
	}
	err = s.asResearcher(ctx, user, "", "research.instrument.draft_read", "survey_instrument", id, nil, func(db Store) error {
		inst, e := db.LockInstrument(ctx, id)
		if e != nil {
			return e
		}
		d, e = db.Draft(ctx, inst)
		return e
	})
	return d, err
}

// CreateDraft abre una nueva versión copiando la última publicada. Falla con
// ErrDraftExists si ya hay un borrador.
func (s *Service) CreateDraft(ctx context.Context, user, id string) (d domain.Draft, err error) {
	if id = strings.ToLower(id); !domain.ValidID(id) {
		return d, domain.ErrInstrumentNotFound
	}
	err = s.asResearcher(ctx, user, "", "research.instrument.draft_created", "survey_instrument", id, nil, func(db Store) error {
		inst, e := db.LockInstrument(ctx, id)
		if e != nil {
			return e
		}
		if _, e = db.Draft(ctx, inst); e == nil {
			return domain.ErrDraftExists
		} else if e != domain.ErrDraftNotFound {
			return e
		}
		d, e = db.CreateDraft(ctx, user, inst)
		return e
	})
	return d, err
}

// ReplaceDraft reemplaza todas las preguntas del borrador con control optimista.
func (s *Service) ReplaceDraft(ctx context.Context, user, id string, rowVersion int, questions []domain.QuestionInput) (d domain.Draft, err error) {
	if id = strings.ToLower(id); !domain.ValidID(id) {
		return d, domain.ErrInstrumentNotFound
	}
	qs, err := domain.NormalizeQuestions(questions)
	if err != nil {
		return d, err
	}
	err = s.asResearcher(ctx, user, "", "research.instrument.draft_saved", "survey_instrument", id, nil, func(db Store) error {
		inst, e := db.LockInstrument(ctx, id)
		if e != nil {
			return e
		}
		draft, e := db.Draft(ctx, inst)
		if e != nil {
			return e
		}
		d, e = db.ReplaceDraft(ctx, user, draft, rowVersion, qs)
		return e
	})
	return d, err
}

// PublishDraft publica el borrador (queda inmutable) y archiva la versión anterior.
// En el instrumento CAP exige exactamente 8 opción múltiple, 8 Likert y 10 de frecuencia.
func (s *Service) PublishDraft(ctx context.Context, user, id string) (v domain.Version, err error) {
	if id = strings.ToLower(id); !domain.ValidID(id) {
		return v, domain.ErrInstrumentNotFound
	}
	err = s.asResearcher(ctx, user, "", "research.instrument.published", "survey_instrument", id, nil, func(db Store) error {
		inst, e := db.LockInstrument(ctx, id)
		if e != nil {
			return e
		}
		draft, e := db.Draft(ctx, inst)
		if e != nil {
			return e
		}
		if e = domain.CheckPublishable(draft.Questions, inst.IsCAP); e != nil {
			return e
		}
		v, e = db.PublishDraft(ctx, user, draft, s.now().UTC())
		return e
	})
	return v, err
}

// ---- Resultados, exportación y necesidades ----

// capVersion resuelve la versión pedida (o la CAP publicada si versionID es vacío).
func capVersion(ctx context.Context, db Store, versionID string) (*domain.SurveyVersion, error) {
	if versionID == "" {
		v, err := db.PublishedCAP(ctx)
		if err == nil && v == nil {
			err = domain.ErrSurveyUnavailable
		}
		return v, err
	}
	versionID = strings.ToLower(versionID)
	if !domain.ValidID(versionID) {
		return nil, domain.ErrVersionNotFound
	}
	return db.CAPVersion(ctx, versionID)
}

// Results devuelve los resultados consolidados por empresa (RF-26) de una versión
// del instrumento CAP: n, medias por dimensión y comparación pre vs post.
func (s *Service) Results(ctx context.Context, user, versionID string, p pagination.Request) (v domain.Version, out pagination.Page[OrganizationResult], err error) {
	scope := []string{user, "research-results", strings.ToLower(versionID)}
	w, err := pagination.Open(p, scope...)
	if err != nil {
		return v, out, err
	}
	err = s.asResearcher(ctx, user, "", "research.results.read", "survey_instrument_version", strings.ToLower(versionID), nil, func(db Store) error {
		sv, e := capVersion(ctx, db, versionID)
		if e != nil {
			return e
		}
		v = sv.Version
		items, e := db.OrganizationResults(ctx, sv.Version.ID, w)
		out = pagination.Close(items, w, func(r OrganizationResult) pagination.Position { return pagination.Position{ID: r.OrganizationID} }, scope...)
		return e
	})
	return v, out, err
}

// Export genera los datos de la exportación CSV anonimizada y audita cada exportación
// con la versión, el filtro y el número de filas (nunca el contenido).
func (s *Service) Export(ctx context.Context, user, versionID, org string) (data ExportData, err error) {
	if org != "" && !domain.ValidOrganization(org) {
		return data, domain.ErrOrganizationNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		if e := s.researcher(ctx, db, user); e != nil {
			return e
		}
		sv, e := capVersion(ctx, db, versionID)
		if e != nil {
			return e
		}
		if org != "" {
			if ok, e := db.OrganizationExists(ctx, org); e != nil || !ok {
				if e == nil {
					e = domain.ErrOrganizationNotFound
				}
				return e
			}
		}
		rows, e := db.ExportRows(ctx, sv.Version.ID, org, MaxExportRows+1)
		if e != nil {
			return e
		}
		if len(rows) > MaxExportRows {
			return domain.ErrExportTooLarge
		}
		data = ExportData{VersionNumber: sv.Version.Number, Questions: sv.Questions, Rows: rows}
		return db.Audit(ctx, org, user, "research.export.csv", "survey_instrument_version", sv.Version.ID,
			map[string]string{"rows": strconv.Itoa(len(rows)), "organizationFilter": boolText(org != "")})
	})
	return data, err
}

// ListOrganizations lista las empresas con datos de investigación posibles (id y nombre).
func (s *Service) ListOrganizations(ctx context.Context, user string, p pagination.Request) (out pagination.Page[OrganizationRef], err error) {
	scope := []string{user, "research-organizations"}
	w, err := pagination.Open(p, scope...)
	if err != nil {
		return out, err
	}
	err = s.asResearcher(ctx, user, "", "research.organizations.listed", "organization", "", nil, func(db Store) error {
		items, e := db.Organizations(ctx, w)
		out = pagination.Close(items, w, func(o OrganizationRef) pagination.Position { return pagination.Position{ID: o.ID} }, scope...)
		return e
	})
	return out, err
}

// AddNeed registra una necesidad detectada en una empresa (RF-50).
func (s *Service) AddNeed(ctx context.Context, user string, in domain.NeedInput) (n domain.Need, err error) {
	if in, err = domain.NormalizeNeed(in, s.now().UTC()); err != nil {
		return n, err
	}
	err = s.asResearcher(ctx, user, in.OrganizationID, "research.need.created", "research_company_need", "", map[string]string{"category": in.Category}, func(db Store) error {
		ok, e := db.OrganizationExists(ctx, in.OrganizationID)
		if e != nil {
			return e
		}
		if !ok {
			return domain.ErrOrganizationNotFound
		}
		n, e = db.AddNeed(ctx, user, in)
		return e
	})
	return n, err
}

// ListNeeds lista las necesidades, de la más reciente a la más antigua, con filtro opcional por empresa.
func (s *Service) ListNeeds(ctx context.Context, user, org string, p pagination.Request) (out pagination.Page[domain.Need], err error) {
	if org != "" && !domain.ValidOrganization(org) {
		return out, domain.ErrOrganizationNotFound
	}
	scope := []string{user, "research-needs", org}
	w, err := pagination.Open(p, scope...)
	if err != nil {
		return out, err
	}
	if err = validUUIDCursor(w); err != nil {
		return out, err
	}
	if w.After.Key != "" {
		if _, e := time.Parse(time.RFC3339Nano, w.After.Key); e != nil {
			return out, pagination.ErrInvalid
		}
	}
	err = s.asResearcher(ctx, user, org, "research.need.listed", "research_company_need", "", nil, func(db Store) error {
		if org != "" {
			if ok, e := db.OrganizationExists(ctx, org); e != nil || !ok {
				if e == nil {
					e = domain.ErrOrganizationNotFound
				}
				return e
			}
		}
		items, e := db.Needs(ctx, org, w)
		out = pagination.Close(items, w, func(n domain.Need) pagination.Position {
			return pagination.Position{Key: n.DetectedAt.UTC().Format(time.RFC3339Nano), ID: n.ID}
		}, scope...)
		return e
	})
	return out, err
}
