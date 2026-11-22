// Package postgres guarda la normativa y el registro de consultas del agente normativo (V17).
package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

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
	rows, err := r.pool.Query(ctx, `SELECT d.id, d.content, f.search_vector FROM normative_documents d CROSS JOIN normative_fragments f LIMIT 0`)
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

func (r *Repository) ActiveCount(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM normative_documents WHERE status='ACTIVE'`).Scan(&n)
	return n, err
}

func (r *Repository) Document(ctx context.Context, id string) (domain.Document, error) {
	return scan(r.pool.QueryRow(ctx, `SELECT `+columns+` FROM normative_documents WHERE id=$1`, id))
}

// saveWithFragments guarda la norma y reemplaza sus fragmentos en la misma transacción.
func (r *Repository) saveWithFragments(ctx context.Context, query string, args []any, fragments []string) (domain.Document, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Document{}, err
	}
	defer tx.Rollback(context.Background())
	doc, err := scan(tx.QueryRow(ctx, query, args...))
	if err != nil {
		return doc, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM normative_fragments WHERE document_id=$1`, doc.ID); err != nil {
		return doc, err
	}
	batch := &pgx.Batch{}
	for i, text := range fragments {
		batch.Queue(`INSERT INTO normative_fragments(document_id,position,body) VALUES($1,$2,$3)`, doc.ID, i, text)
	}
	if len(fragments) > 0 {
		if err = tx.SendBatch(ctx, batch).Close(); err != nil {
			return doc, err
		}
	}
	return doc, tx.Commit(ctx)
}

func (r *Repository) CreateDocument(ctx context.Context, user, id string, in domain.DocumentInput, fragments []string) (domain.Document, error) {
	return r.saveWithFragments(ctx, `INSERT INTO normative_documents(id,title,source,content,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$5) RETURNING `+columns,
		[]any{id, in.Title, in.Source, in.Content, user}, fragments)
}

func (r *Repository) UpdateDocument(ctx context.Context, user, id string, in domain.DocumentInput, fragments []string) (domain.Document, error) {
	return r.saveWithFragments(ctx, `UPDATE normative_documents SET title=$2,source=$3,content=$4,updated_by=$5,updated_at=now() WHERE id=$1 RETURNING `+columns,
		[]any{id, in.Title, in.Source, in.Content, user}, fragments)
}

func (r *Repository) SetStatus(ctx context.Context, user, id, status string) (domain.Document, error) {
	return scan(r.pool.QueryRow(ctx, `UPDATE normative_documents SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1 RETURNING `+columns, id, status, user))
}

// searchSQL busca con la configuración en español y sin tildes; %s es la consulta tsquery a usar.
const searchSQL = `WITH q AS (SELECT %s AS query)
 SELECT d.id::text, d.title, d.source, f.body FROM normative_fragments f
 JOIN normative_documents d ON d.id=f.document_id AND d.status='ACTIVE' CROSS JOIN q
 WHERE f.search_vector @@ q.query ORDER BY ts_rank(f.search_vector, q.query) DESC, d.created_at, f.position LIMIT $2`

// allWords exige todas las palabras; anyWord acepta cualquiera (se usa si la primera no encuentra nada).
const allWords = `plainto_tsquery('spanish'::regconfig, translate(lower($1), 'áéíóúüñ', 'aeiouun'))`
const anyWord = `NULLIF(replace(plainto_tsquery('spanish'::regconfig, translate(lower($1), 'áéíóúüñ', 'aeiouun'))::text, ' & ', ' | '), '')::tsquery`

// Search ordena por pertinencia y luego por documento y posición.
func (r *Repository) Search(ctx context.Context, query string, limit int) ([]domain.SearchResult, error) {
	results, err := r.search(ctx, fmt.Sprintf(searchSQL, allWords), query, limit)
	if err != nil || len(results) > 0 {
		return results, err
	}
	return r.search(ctx, fmt.Sprintf(searchSQL, anyWord), query, limit)
}

func (r *Repository) search(ctx context.Context, sql, query string, limit int) ([]domain.SearchResult, error) {
	rows, err := r.pool.Query(ctx, sql, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SearchResult{}
	for rows.Next() {
		var v domain.SearchResult
		if err = rows.Scan(&v.DocumentID, &v.DocumentTitle, &v.DocumentSource, &v.Text); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
