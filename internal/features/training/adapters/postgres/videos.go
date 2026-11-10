package postgres

import (
	"context"
	"errors"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/jackc/pgx/v5"
)

// Video lee un video de capacitación de la organización: sin sede y bajo la
// clave privada organizations/{org}/training/videos/. Cualquier otro archivo es 404.
func (s *store) Video(ctx context.Context, org, id string, lock bool) (domain.VideoFile, error) {
	clause := ""
	if lock {
		clause = " FOR UPDATE OF m"
	}
	file, err := scanVideo(s.tx.QueryRow(ctx, `SELECT `+videoColumns+` FROM media_files m WHERE m.organization_id=$1 AND m.id=$2 AND m.location_id IS NULL AND starts_with(m.object_key,$3)`+clause, org, id, trainingVideoPrefix(org)))
	if errors.Is(err, pgx.ErrNoRows) {
		return file, domain.ErrVideoNotFound
	}
	return file, err
}

func (s *store) ReserveVideo(ctx context.Context, org, user string, f domain.VideoFile) (domain.VideoFile, error) {
	existing, err := s.Video(ctx, org, f.ID, true)
	if err == nil {
		return existing, existing.Match(domain.VideoUploadInput{ID: f.ID, OriginalName: f.OriginalName, MIMEType: f.MIMEType, SizeBytes: f.SizeBytes, SHA256: f.SHA256})
	}
	if !errors.Is(err, domain.ErrVideoNotFound) {
		return domain.VideoFile{}, err
	}
	tag, err := s.tx.Exec(ctx, `INSERT INTO media_files(id,organization_id,location_id,object_key,storage_url,original_name,mime_type,size_bytes,sha256,created_by,updated_by)
 VALUES($1,$2,NULL,$3,$4,$5,$6,$7,$8,$9,$9) ON CONFLICT DO NOTHING`, f.ID, org, f.ObjectKey, "r2://"+f.ObjectKey, f.OriginalName, f.MIMEType, f.SizeBytes, f.SHA256, user)
	if err != nil {
		return domain.VideoFile{}, err
	}
	// El id (o la clave) ya existe fuera de esta organización: no se revela, es conflicto.
	if tag.RowsAffected() != 1 {
		return domain.VideoFile{}, domain.ErrVideoConflict
	}
	if err = s.Audit(ctx, org, user, "training.video.reserved", "media_file", f.ID); err != nil {
		return domain.VideoFile{}, err
	}
	return s.Video(ctx, org, f.ID, false)
}

func (s *store) SetVideoStatus(ctx context.Context, org, user string, f domain.VideoFile, status string) (domain.VideoFile, error) {
	action := map[string]string{"AVAILABLE": "training.video.confirmed", "FAILED": "training.video.failed"}[status]
	if action == "" {
		return domain.VideoFile{}, domain.ErrVideoConflict
	}
	if _, err := s.tx.Exec(ctx, `UPDATE media_files SET status=$3::varchar(24),uploaded_at=CASE WHEN $3::varchar(24)='AVAILABLE' THEN NOW() ELSE uploaded_at END,updated_by=$4 WHERE organization_id=$1 AND id=$2`, org, f.ID, status, user); err != nil {
		return domain.VideoFile{}, err
	}
	if err := s.Audit(ctx, org, user, action, "media_file", f.ID); err != nil {
		return domain.VideoFile{}, err
	}
	return s.Video(ctx, org, f.ID, false)
}
