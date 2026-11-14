package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/Nidka/BPM-service/internal/features/training/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
)

func (s *store) InsertPath(ctx context.Context, org, user string, in domain.PathInput) error {
	_, err := s.tx.Exec(ctx, `INSERT INTO learning_paths(id,organization_id,title,description,status,created_by,updated_by) VALUES($1,$2,$3,NULLIF($4,''),'DRAFT',$5,$5)`, in.ID, org, in.Title, in.Description, user)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.ErrIdempotencyConflict
		}
		return err
	}
	return s.insertPathCourses(ctx, org, user, in.ID, in.CourseIDs)
}

func (s *store) insertPathCourses(ctx context.Context, org, user, pathID string, courseIDs []string) error {
	batch := &pgx.Batch{}
	for i, id := range courseIDs {
		batch.Queue(`INSERT INTO learning_path_courses(organization_id,path_id,course_id,position,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$5)`, org, pathID, id, i+1, user)
	}
	if batch.Len() == 0 {
		return nil
	}
	br := s.tx.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

// pathCourses lee los cursos de varias rutas con el progreso del usuario (vacío = sin progreso).
func (s *store) pathCourses(ctx context.Context, org, userID string, paths map[string]*domain.Path) error {
	ids := make([]string, 0, len(paths))
	for id := range paths {
		ids = append(ids, id)
	}
	rows, err := s.tx.Query(ctx, `SELECT pc.path_id::text,pc.course_id::text,c.code,c.title,pc.position,c.status,COALESCE(p.status,'NOT_STARTED')
 FROM learning_path_courses pc JOIN training_courses c ON c.organization_id=pc.organization_id AND c.id=pc.course_id
 LEFT JOIN training_progress p ON p.organization_id=pc.organization_id AND p.course_id=pc.course_id AND p.user_id=$3
 WHERE pc.organization_id=$1 AND pc.path_id::text=ANY($2::text[]) ORDER BY pc.path_id,pc.position`, org, ids, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pathID string
		var c domain.PathCourse
		if err = rows.Scan(&pathID, &c.CourseID, &c.Code, &c.Title, &c.Position, &c.CourseStatus, &c.Progress); err != nil {
			return err
		}
		paths[pathID].Courses = append(paths[pathID].Courses, c)
	}
	return rows.Err()
}

const pathColumns = `id::text,title,COALESCE(description,''),status,row_version,published_at`

func scanPath(row pgx.Row) (p domain.Path, err error) {
	err = row.Scan(&p.ID, &p.Title, &p.Description, &p.Status, &p.RowVersion, &p.PublishedAt)
	if p.PublishedAt != nil {
		t := p.PublishedAt.UTC()
		p.PublishedAt = &t
	}
	p.Courses = []domain.PathCourse{}
	return
}

func (s *store) FindPath(ctx context.Context, org, userID, id string, lock bool) (domain.Path, bool, error) {
	query := `SELECT ` + pathColumns + ` FROM learning_paths WHERE organization_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	p, err := scanPath(s.tx.QueryRow(ctx, query, org, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	err = s.pathCourses(ctx, org, userID, map[string]*domain.Path{p.ID: &p})
	return p, err == nil, err
}

func (s *store) ListPaths(ctx context.Context, org, userID, status string, w pagination.Window) ([]domain.Path, error) {
	var afterTitle, afterID *string
	if w.After.ID != "" {
		afterTitle, afterID = &w.After.Key, &w.After.ID
	}
	rows, err := s.tx.Query(ctx, `SELECT `+pathColumns+` FROM learning_paths WHERE organization_id=$1 AND ($2='' OR status=$2)
 AND ($3::text IS NULL OR (title,id::text)>($3,$4)) ORDER BY title,id::text LIMIT $5`, org, status, afterTitle, afterID, w.Size+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Path{}
	for rows.Next() {
		p, e := scanPath(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	index := make(map[string]*domain.Path, len(out))
	for i := range out {
		index[out[i].ID] = &out[i]
	}
	if len(index) > 0 {
		if err = s.pathCourses(ctx, org, userID, index); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *store) ReplacePath(ctx context.Context, org, user, id string, rowVersion int, in domain.PathInput) error {
	tag, err := s.tx.Exec(ctx, `UPDATE learning_paths SET title=$4,description=NULLIF($5,''),row_version=row_version+1,updated_by=$6 WHERE organization_id=$1 AND id=$2 AND row_version=$3 AND status<>'ARCHIVED'`,
		org, id, rowVersion, in.Title, in.Description, user)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrPathConflict
	}
	if _, err = s.tx.Exec(ctx, `DELETE FROM learning_path_courses WHERE organization_id=$1 AND path_id=$2`, org, id); err != nil {
		return err
	}
	return s.insertPathCourses(ctx, org, user, id, in.CourseIDs)
}

func (s *store) SetPathStatus(ctx context.Context, org, user, id, status string, now time.Time) error {
	_, err := s.tx.Exec(ctx, `UPDATE learning_paths SET status=$3::varchar,published_at=CASE WHEN $3::varchar='PUBLISHED' THEN COALESCE(published_at,$4) ELSE published_at END,row_version=row_version+1,updated_by=$5 WHERE organization_id=$1 AND id=$2`, org, id, status, now, user)
	return err
}

func (s *store) CourseStatuses(ctx context.Context, org string, ids []string) (map[string]string, error) {
	rows, err := s.tx.Query(ctx, `SELECT id::text,status FROM training_courses WHERE organization_id=$1 AND id::text=ANY($2::text[])`, org, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, status string
		if err = rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		out[id] = status
	}
	return out, rows.Err()
}

// CertificateByCode devuelve solo los datos mínimos de verificación pública. Las
// constancias revocadas siguen existiendo y se informan como REVOKED.
func (s *store) CertificateByCode(ctx context.Context, code string) (domain.PublicCertificate, error) {
	var out domain.PublicCertificate
	var revoked *time.Time
	var display string
	err := s.tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(o.trade_name,''),o.legal_name),c.title,v.version_number,cert.issued_at,cert.revoked_at,u.display_name
 FROM training_certificates cert
 JOIN training_course_versions v ON v.organization_id=cert.organization_id AND v.id=cert.course_version_id
 JOIN training_courses c ON c.organization_id=v.organization_id AND c.id=v.course_id
 JOIN organizations o ON o.id=cert.organization_id
 JOIN application_users u ON u.id=cert.user_id
 WHERE cert.verification_code=$1`, code).Scan(&out.Organization, &out.CourseTitle, &out.VersionNumber, &out.IssuedAt, &revoked, &display)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, domain.ErrCertificateNotFound
	}
	if err != nil {
		return out, err
	}
	out.IssuedAt = out.IssuedAt.UTC()
	out.Holder = domain.AbbreviateHolder(display)
	out.Status = domain.CertificateValid
	if revoked != nil {
		out.Status = domain.CertificateRevoked
	}
	return out, nil
}
