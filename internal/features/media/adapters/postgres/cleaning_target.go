package postgres

import (
	"context"
	"errors"

	"github.com/Nidka/BPM-service/internal/features/media/domain"
	"github.com/jackc/pgx/v5"
)

// cleaningEditWindow es el plazo, desde el registro, en el que quien ejecutó la limpieza puede
// adjuntar o retirar fotos. Después la evidencia queda solo de lectura.
const cleaningEditWindow = `24 hours`

// CleaningTarget bloquea la ejecución de limpieza para serializar altas, retiros y límite de fotos.
func (s *store) CleaningTarget(ctx context.Context, user, org, execution string, write bool) (t domain.Target, err error) {
	role, err := s.actor(ctx, user, org)
	if err != nil {
		return t, err
	}
	t = domain.Target{OrganizationID: org, UserID: user, Role: role, CleaningID: execution}
	clause := " FOR SHARE OF e,l"
	if write {
		clause = " FOR UPDATE OF e FOR SHARE OF l"
	}
	err = s.tx.QueryRow(ctx, `SELECT e.location_id::text,e.responsible_user_id,(e.created_at + INTERVAL '`+cleaningEditWindow+`' > NOW()) FROM cleaning_executions e
 JOIN locations l ON l.organization_id=e.organization_id AND l.id=e.location_id AND l.status='ACTIVE' WHERE e.organization_id=$1 AND e.id=$2`+clause, org, execution).Scan(&t.LocationID, &t.CleaningOwner, &t.CleaningOpen)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if role != "ADMIN" && role != "SUPER_ADMIN" {
		var site string
		err = s.tx.QueryRow(ctx, `SELECT location_id::text FROM membership_locations WHERE organization_id=$1 AND user_id=$2 AND location_id=$3 FOR SHARE`, org, user, t.LocationID).Scan(&site)
		if errors.Is(err, pgx.ErrNoRows) {
			return t, domain.ErrDenied
		}
	}
	return t, err
}

// cleaningFileFields coloca la ejecución en la posición de la revisión; cleaningFile la reubica.
const cleaningFileFields = `m.id::text,m.organization_id,COALESCE(m.location_id::text,''),cl.cleaning_execution_id::text,''::text,m.original_name,m.mime_type,m.size_bytes,COALESCE(m.sha256,''),m.object_key,m.status,COALESCE(m.created_by,''),m.created_at,m.uploaded_at,''::text,''::text,''::text`

func cleaningFile(f domain.File, err error) (domain.File, error) {
	f.CleaningID, f.RunID = f.RunID, ""
	return f, err
}

func (s *store) cleaningList(ctx context.Context, t domain.Target) ([]domain.File, error) {
	rows, err := s.tx.Query(ctx, `SELECT `+cleaningFileFields+` FROM cleaning_media cl JOIN media_files m ON m.organization_id=cl.organization_id AND m.id=cl.media_file_id WHERE cl.organization_id=$1 AND cl.cleaning_execution_id=$2 AND m.status<>'DELETED' AND m.location_id=$3`+unambiguousFile+` ORDER BY m.created_at,m.id`, t.OrganizationID, t.CleaningID, t.LocationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.File{}
	for rows.Next() {
		f, e := cleaningFile(scanFile(rows))
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
