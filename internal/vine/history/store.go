// Package history provides session history indexing and search using pgvector.
// Completed sessions are indexed into the knowledge_entries table for semantic search.
package history

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Vector is a float32 slice representing an embedding vector.
type Vector = []float32

// Embedder generates embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, text string) (Vector, error)
}

// Entry represents a knowledge entry indexed from session history.
type Entry struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Content   string    `json:"content"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionSummary is a brief overview of a session for search results.
type SessionSummary struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	SourceID  string    `json:"source_id"`
	Status    string    `json:"status"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store provides history indexing and search.
type Store struct {
	pool   *pgxpool.Pool
	embeds Embedder
}

// NewStore creates a new history store.
func NewStore(pool *pgxpool.Pool, embeds Embedder) *Store {
	return &Store{pool: pool, embeds: embeds}
}

// IndexSession creates a knowledge entry from a completed session.
// It generates an embedding from the provided summary text and stores it
// in the knowledge_entries table.
func (s *Store) IndexSession(ctx context.Context, sessionID, content string) (*Entry, error) {
	vec, err := s.embeds.Embed(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("generating embedding for session %s: %w", sessionID, err)
	}

	return s.IndexWithEmbedding(ctx, sessionID, content, vec)
}

// IndexWithEmbedding creates a knowledge entry with a pre-computed embedding.
func (s *Store) IndexWithEmbedding(ctx context.Context, sessionID, content string, embedding Vector) (*Entry, error) {
	var entry Entry
	err := s.pool.QueryRow(ctx, `
		INSERT INTO knowledge_entries (session_id, content, embedding, metadata)
		VALUES ($1, $2, $3, '{}')
		RETURNING id, session_id, content, metadata, created_at
	`, sessionID, content, formatVector(embedding)).Scan(
		&entry.ID, &entry.SessionID, &entry.Content, &entry.Metadata, &entry.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("indexing session %s: %w", sessionID, err)
	}
	return &entry, nil
}

// Search performs vector similarity search for history entries.
func (s *Store) Search(ctx context.Context, queryEmbedding Vector, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 5
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, content, metadata, created_at,
		       1 - (embedding <=> $1) AS similarity
		FROM knowledge_entries
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1
		LIMIT $2
	`, formatVector(queryEmbedding), limit)
	if err != nil {
		return nil, fmt.Errorf("searching history: %w", err)
	}
	defer rows.Close()

	var results []Entry
	for rows.Next() {
		var entry Entry
		var similarity float64
		if err := rows.Scan(
			&entry.ID, &entry.SessionID, &entry.Content, &entry.Metadata, &entry.CreatedAt,
			&similarity,
		); err != nil {
			return nil, fmt.Errorf("scanning entry: %w", err)
		}
		results = append(results, entry)
	}
	return results, rows.Err()
}

// SearchByText generates an embedding for the query and searches.
func (s *Store) SearchByText(ctx context.Context, query string, limit int) ([]Entry, error) {
	vec, err := s.embeds.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("generating query embedding: %w", err)
	}
	return s.Search(ctx, vec, limit)
}

// SearchByFilter performs structured search on sessions.
func (s *Store) SearchByFilter(ctx context.Context, filters map[string]interface{}, limit, offset int) ([]SessionSummary, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	// Build dynamic query
	where := "WHERE 1=1"
	args := []interface{}{}
	argIdx := 1

	if source, ok := filters["source"].(string); ok && source != "" {
		where += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, source)
		argIdx++
	}
	if sourceID, ok := filters["source_id"].(string); ok && sourceID != "" {
		where += fmt.Sprintf(" AND source_id = $%d", argIdx)
		args = append(args, sourceID)
		argIdx++
	}
	if status, ok := filters["status"].(string); ok && status != "" {
		where += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}
	if since, ok := filters["since"].(time.Time); ok {
		where += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, since)
		argIdx++
	}
	if until, ok := filters["until"].(time.Time); ok {
		where += fmt.Sprintf(" AND created_at <= $%d", argIdx)
		args = append(args, until)
		argIdx++
	}

	query := fmt.Sprintf(`
		SELECT id, source, source_id, status, metadata, created_at, updated_at
		FROM sessions %s
		ORDER BY updated_at DESC
		LIMIT $%d OFFSET $%d
	`, where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching sessions: %w", err)
	}
	defer rows.Close()

	var results []SessionSummary
	for rows.Next() {
		var s SessionSummary
		if err := rows.Scan(&s.ID, &s.Source, &s.SourceID, &s.Status, &s.Metadata, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// DeleteBySession removes all knowledge entries for a session.
func (s *Store) DeleteBySession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE session_id = $1`, sessionID)
	if err != nil {
		return fmt.Errorf("deleting history for session %s: %w", sessionID, err)
	}
	return nil
}

// Count returns the total number of knowledge entries.
func (s *Store) Count(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_entries`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting entries: %w", err)
	}
	return count, nil
}

// formatVector formats a float32 slice as a pgvector-compatible string.
func formatVector(v Vector) string {
	if v == nil {
		return ""
	}
	result := make([]byte, 0, len(v)*12+2)
	result = append(result, '[')
	for i, f := range v {
		if i > 0 {
			result = append(result, ',')
		}
		result = fmt.Appendf(result, "%.6f", f)
	}
	result = append(result, ']')
	return string(result)
}
