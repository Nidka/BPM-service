// Package postgres guarda la normativa y el registro de consultas del agente normativo (V17).
package postgres

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Nidka/BPM-service/internal/features/normative/application"
	"github.com/Nidka/BPM-service/internal/features/normative/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository implementa application.Store; cada operación es una sola sentencia.
type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

var _ application.Store = (*Repository)(nil)

// RequireSchema falla de forma explícita si falta la migración V17.
func (r *Repository) RequireSchema(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT d.id, d.content, q.citations FROM normative_documents d CROSS JOIN normative_questions q LIMIT 0`)
	if err != nil {
		return errors.New("normative migration V17 is required")
	}
	rows.Close()
	return rows.Err()
}

// NewID genera un UUID v4.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (r *Repository) PlatformRole(ctx context.Context, user string) (string, error) {
	var role *string
	err := r.pool.QueryRow(ctx, `SELECT platform_role FROM application_users WHERE id=$1 AND status='ACTIVE'`, user).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrDenied
	}
	if err != nil {
		return "", err
	}
	if role == nil {
		return "", nil
	}
	return *role, nil
}

const columns = `id::text,title,source,content,status,char_length(content),created_at,updated_at`

func scan(row pgx.Row) (domain.Document, error) {
	var d domain.Document
	err := row.Scan(&d.ID, &d.Title, &d.Source, &d.Content, &d.Status, &d.Characters, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, domain.ErrNotFound
	}
	return d, err
}

func (r *Repository) list(ctx context.Context, query string, args ...any) ([]domain.Document, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Document{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repository) Documents(ctx context.Context, status string) ([]domain.Document, error) {
	return r.list(ctx, `SELECT `+columns+` FROM normative_documents WHERE status=$1 ORDER BY updated_at DESC, id LIMIT 200`, status)
}

// ActiveDocuments devuelve la normativa activa en orden estable (por fecha de carga) para no romper la caché.
func (r *Repository) ActiveDocuments(ctx context.Context) ([]domain.Document, error) {
	return r.list(ctx, `SELECT `+columns+` FROM normative_documents WHERE status='ACTIVE' ORDER BY created_at, id`)
}

func (r *Repository) ActiveCharacters(ctx context.Context, excludeID string) (int, error) {
	var total int
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(sum(char_length(content)),0) FROM normative_documents WHERE status='ACTIVE' AND ($1='' OR id::text<>$1)`, excludeID).Scan(&total)
	return total, err
}

func (r *Repository) Document(ctx context.Context, id string) (domain.Document, error) {
	return scan(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM normative_documents WHERE id=$1`, id))
}

func (r *Repository) CreateDocument(ctx context.Context, user, id string, in domain.DocumentInput) (domain.Document, error) {
	return scan(r.pool.QueryRow(ctx, `INSERT INTO normative_documents(id,title,source,content,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$5) RETURNING `+columns,
		id, in.Title, in.Source, in.Content, user))
}

func (r *Repository) UpdateDocument(ctx context.Context, user, id string, in domain.DocumentInput) (domain.Document, error) {
	return scan(r.pool.QueryRow(ctx, `UPDATE normative_documents SET title=$2,source=$3,content=$4,updated_by=$5,updated_at=now() WHERE id=$1 RETURNING `+columns,
		id, in.Title, in.Source, in.Content, user))
}

func (r *Repository) SetStatus(ctx context.Context, user, id, status string) (domain.Document, error) {
	return scan(r.pool.QueryRow(ctx, `UPDATE normative_documents SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1 RETURNING `+columns, id, status, user))
}

func (r *Repository) QuestionsSince(ctx context.Context, user string, since time.Time) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM normative_questions WHERE user_id=$1 AND created_at>=$2`, user, since).Scan(&n)
	return n, err
}

func (r *Repository) LogQuestion(ctx context.Context, user, question string, answer domain.Answer) error {
	if answer.Citations == nil {
		answer.Citations = []domain.Citation{}
	}
	citations, err := json.Marshal(answer.Citations)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO normative_questions(user_id,question,answer,citations,declined) VALUES($1,$2,$3,$4::jsonb,$5)`,
		user, question, answer.Text, string(citations), answer.Declined)
	return err
}
