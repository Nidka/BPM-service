package postgres

import (
	"context"
	"errors"
	"strconv"

	"github.com/Nidka/BPM-service/internal/features/checklists/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

// lockActiveTemplate bloquea la plantilla: publicar versiones, programar e iniciar revisiones se serializan.
func (s *store) lockActiveTemplate(ctx context.Context, org, id string) error {
	var found string
	err := s.tx.QueryRow(ctx, `SELECT id::text FROM checklist_templates WHERE organization_id=$1 AND id=$2 AND status='ACTIVE' FOR UPDATE`, org, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// PublishVersion crea la versión N+1 inmutable y archiva la anterior en una sola transacción.
// Las revisiones ya iniciadas conservan su template_version_id; las programaciones leen siempre
// la versión PUBLISHED vigente. Repetir el mismo UUID y contenido devuelve la versión existente.
func (s *store) PublishVersion(ctx context.Context, a domain.Actor, org, templateID string, v domain.NewVersion, hash string) (domain.Template, error) {
	if err := s.lockActiveTemplate(ctx, org, templateID); err != nil {
		return domain.Template{}, err
	}
	var existingTemplate, existingHash string
	var number int
	err := s.tx.QueryRow(ctx, `SELECT template_id::text,version_number,COALESCE(request_hash,'') FROM checklist_template_versions WHERE organization_id=$1 AND id=$2`, org, v.VersionID).Scan(&existingTemplate, &number, &existingHash)
	if err == nil {
		if existingTemplate != templateID || number != v.BaseVersionNumber+1 || existingHash != hash {
			return domain.Template{}, domain.ErrConflict
		}
		return s.Version(ctx, org, templateID, v.VersionID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Template{}, err
	}
	var latest int
	if err = s.tx.QueryRow(ctx, `SELECT COALESCE(max(version_number),0) FROM checklist_template_versions WHERE organization_id=$1 AND template_id=$2`, org, templateID).Scan(&latest); err != nil {
		return domain.Template{}, err
	}
	if latest != v.BaseVersionNumber {
		return domain.Template{}, domain.ErrVersionConflict
	}
	if _, err = s.tx.Exec(ctx, `UPDATE checklist_template_versions SET status='ARCHIVED',updated_by=$3 WHERE organization_id=$1 AND template_id=$2 AND status='PUBLISHED'`, org, templateID, a.ID); err != nil {
		return domain.Template{}, err
	}
	if _, err = s.tx.Exec(ctx, `INSERT INTO checklist_template_versions(id,organization_id,template_id,version_number,status,published_at,change_note,request_hash,created_by,updated_by)
 VALUES($1,$2,$3,$4,'PUBLISHED',NOW(),NULLIF($5,''),$6,$7,$7)`, v.VersionID, org, templateID, latest+1, v.ChangeNote, hash, a.ID); err != nil {
		return domain.Template{}, pgConflict(err)
	}
	for _, i := range v.Items {
		_, err = s.tx.Exec(ctx, `INSERT INTO checklist_items(id,organization_id,template_version_id,section_name,label,response_type,display_order,required,minimum_value,maximum_value,unit,evidence_required_on_failure,created_by,updated_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$13)`, i.ID, org, v.VersionID, i.SectionName, i.Label, i.ResponseType, i.DisplayOrder, i.Required, i.MinimumValue, i.MaximumValue, i.Unit, i.EvidenceRequiredOnFailure, a.ID)
		if err != nil {
			return domain.Template{}, pgConflict(err)
		}
	}
	if err = s.audit(ctx, a, org, "checklist.template.version_published", "checklist_template", templateID); err != nil {
		return domain.Template{}, err
	}
	return s.Version(ctx, org, templateID, v.VersionID)
}

// Version devuelve una versión publicada o archivada con todas sus preguntas.
func (s *store) Version(ctx context.Context, org, templateID, versionID string) (domain.Template, error) {
	var t domain.Template
	err := s.tx.QueryRow(ctx, `SELECT t.id::text,t.name,COALESCE(t.description,''),t.area,v.id::text,v.version_number
 FROM checklist_template_versions v JOIN checklist_templates t ON t.organization_id=v.organization_id AND t.id=v.template_id
 WHERE v.organization_id=$1 AND v.template_id=$2 AND v.id=$3 AND t.status='ACTIVE' AND v.status IN ('PUBLISHED','ARCHIVED')`, org, templateID, versionID).Scan(&t.ID, &t.Name, &t.Description, &t.Area, &t.VersionID, &t.VersionNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	rows, err := s.tx.Query(ctx, `SELECT id::text,section_name,label,response_type,display_order,required,minimum_value::text,maximum_value::text,COALESCE(unit,''),evidence_required_on_failure
 FROM checklist_items WHERE organization_id=$1 AND template_version_id=$2 ORDER BY display_order`, org, versionID)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	t.Items = []domain.Item{}
	for rows.Next() {
		var i domain.Item
		var min, max *string
		if err = rows.Scan(&i.ID, &i.SectionName, &i.Label, &i.ResponseType, &i.DisplayOrder, &i.Required, &min, &max, &i.Unit, &i.EvidenceRequiredOnFailure); err != nil {
			return t, err
		}
		if i.MinimumValue, err = parseNullable(min); err != nil {
			return t, err
		}
		if i.MaximumValue, err = parseNullable(max); err != nil {
			return t, err
		}
		t.Items = append(t.Items, i)
	}
	return t, rows.Err()
}

// Versions lista las versiones de la más reciente a la más antigua, sin descargar preguntas.
func (s *store) Versions(ctx context.Context, org, templateID string, w pagination.Window) ([]domain.Version, error) {
	var found string
	if err := s.tx.QueryRow(ctx, `SELECT id::text FROM checklist_templates WHERE organization_id=$1 AND id=$2 AND status='ACTIVE'`, org, templateID).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	after := 0
	if w.After.ID != "" {
		n, e := strconv.Atoi(w.After.Key)
		if e != nil || n < 1 {
			return nil, pagination.ErrInvalid
		}
		after = n
	}
	rows, err := s.tx.Query(ctx, `SELECT v.id::text,v.template_id::text,v.status,COALESCE(v.change_note,''),v.version_number,(SELECT count(*) FROM checklist_items i WHERE i.organization_id=v.organization_id AND i.template_version_id=v.id),v.published_at
 FROM checklist_template_versions v WHERE v.organization_id=$1 AND v.template_id=$2 AND v.status IN ('PUBLISHED','ARCHIVED') AND ($3=0 OR v.version_number<$3) ORDER BY v.version_number DESC LIMIT $4`, org, templateID, after, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Version{}
	for rows.Next() {
		var v domain.Version
		if err = rows.Scan(&v.ID, &v.TemplateID, &v.Status, &v.ChangeNote, &v.Number, &v.ItemCount, &v.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
