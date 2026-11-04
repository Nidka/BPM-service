package postgres

import (
	"context"
	"github.com/Nidka/BPM-service/internal/features/incidents/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

const catalogFields = `id::text,code,name,area,normative_criterion,risk_level,evidence_required,version_number`

func scanCatalog(row pgx.Row) (v domain.Catalog, e error) {
	e = row.Scan(&v.ID, &v.Code, &v.Name, &v.Area, &v.NormativeCriterion, &v.RiskLevel, &v.EvidenceRequired, &v.VersionNumber)
	return v, missing(e)
}
func (s *store) Catalog(ctx context.Context, org, id string) (domain.Catalog, error) {
	return scanCatalog(s.tx.QueryRow(ctx, `SELECT `+catalogFields+` FROM incident_catalog WHERE organization_id=$1 AND id=$2`, org, id))
}
func (s *store) Latest(ctx context.Context, org, code string) (domain.Catalog, error) {
	return scanCatalog(s.tx.QueryRow(ctx, `SELECT `+catalogFields+` FROM incident_catalog WHERE organization_id=$1 AND code=$2 ORDER BY version_number DESC LIMIT 1`, org, code))
}
func (s *store) InsertCatalog(ctx context.Context, a domain.Actor, org string, v domain.Catalog) (domain.Catalog, error) {
	_, e := s.tx.Exec(ctx, `INSERT INTO incident_catalog(id,organization_id,code,name,area,normative_criterion,risk_level,evidence_required,version_number,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, v.ID, org, v.Code, v.Name, v.Area, v.NormativeCriterion, v.RiskLevel, v.EvidenceRequired, v.VersionNumber, a.ID)
	if e != nil {
		return v, conflict(e)
	}
	return s.Catalog(ctx, org, v.ID)
}
func (s *store) Catalogs(ctx context.Context, org string, w pagination.Window) ([]domain.Catalog, error) {
	if w.After.ID != "" && !domain.ValidID(w.After.ID) {
		return nil, pagination.ErrInvalid
	}
	rows, e := s.tx.Query(ctx, `WITH latest AS (SELECT DISTINCT ON(code) * FROM incident_catalog WHERE organization_id=$1 ORDER BY code,version_number DESC) SELECT `+catalogFields+` FROM latest WHERE ($2='' OR strpos(lower(name||' '||code||' '||area),lower($2))>0) AND ($3='' OR (lower(name),id::text)>($3,$4)) ORDER BY lower(name),id LIMIT $5`, org, w.Query, w.After.Key, w.After.ID, w.Size+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Catalog{}
	for rows.Next() {
		v, e := scanCatalog(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
