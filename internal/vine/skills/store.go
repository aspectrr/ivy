// Package skills provides a database-backed skill store with pgvector similarity search.
// Skills capture learned patterns from agent sessions for reuse in future sessions.
package skills

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Vector is a float32 slice representing an embedding vector.
type Vector = []float32

// Embedder generates embeddings for text. Implemented by embed.Client.
type Embedder interface {
	Embed(ctx context.Context, text string) (Vector, error)
}

// Skill represents a stored skill with optional embedding.
type Skill struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Content         string    `json:"content"`
	Embedding       Vector    `json:"-"`
	SourceSessionID *string   `json:"source_session_id,omitempty"`
	BuiltIn         bool      `json:"built_in"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SkillSummary is a brief overview for listing.
type SkillSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	BuiltIn     bool   `json:"built_in"`
}

// SkillUsage tracks when a skill was used in a session.
type SkillUsage struct {
	ID         int64     `json:"id"`
	SkillID    string    `json:"skill_id"`
	SessionID  string    `json:"session_id"`
	WasHelpful *bool     `json:"was_helpful,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Store provides database-backed skill storage with vector search.
type Store struct {
	pool   *pgxpool.Pool
	embeds Embedder
}

// NewStore creates a new skills store.
func NewStore(pool *pgxpool.Pool, embeds Embedder) *Store {
	return &Store{pool: pool, embeds: embeds}
}

// Create inserts a new skill with an auto-generated embedding.
func (s *Store) Create(ctx context.Context, name, description, content string, sourceSessionID *string) (*Skill, error) {
	embedText := name + ": " + description
	vec, err := s.embeds.Embed(ctx, embedText)
	if err != nil {
		return nil, fmt.Errorf("generating embedding: %w", err)
	}

	return s.CreateWithEmbedding(ctx, name, description, content, vec, sourceSessionID, false)
}

// CreateWithEmbedding inserts a new skill with a pre-computed embedding.
func (s *Store) CreateWithEmbedding(ctx context.Context, name, description, content string, embedding Vector, sourceSessionID *string, builtIn bool) (*Skill, error) {
	var skill Skill
	err := s.pool.QueryRow(ctx, `
		INSERT INTO skills (name, description, content, embedding, source_session_id, built_in)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, description, content, source_session_id, built_in, created_at, updated_at
	`, name, description, content, formatVector(embedding), sourceSessionID, builtIn).Scan(
		&skill.ID, &skill.Name, &skill.Description, &skill.Content,
		&skill.SourceSessionID, &skill.BuiltIn, &skill.CreatedAt, &skill.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting skill: %w", err)
	}
	return &skill, nil
}

// Get retrieves a skill by ID.
func (s *Store) Get(ctx context.Context, id string) (*Skill, error) {
	var skill Skill
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, content, source_session_id, built_in, created_at, updated_at
		FROM skills WHERE id = $1
	`, id).Scan(
		&skill.ID, &skill.Name, &skill.Description, &skill.Content,
		&skill.SourceSessionID, &skill.BuiltIn, &skill.CreatedAt, &skill.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting skill %s: %w", id, err)
	}
	return &skill, nil
}

// GetByName retrieves a skill by name.
func (s *Store) GetByName(ctx context.Context, name string) (*Skill, error) {
	var skill Skill
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, content, source_session_id, built_in, created_at, updated_at
		FROM skills WHERE name = $1
	`, name).Scan(
		&skill.ID, &skill.Name, &skill.Description, &skill.Content,
		&skill.SourceSessionID, &skill.BuiltIn, &skill.CreatedAt, &skill.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting skill %q: %w", name, err)
	}
	return &skill, nil
}

// Search performs vector similarity search for skills matching the query embedding.
// Returns skills ordered by cosine similarity (most similar first).
func (s *Store) Search(ctx context.Context, queryEmbedding Vector, limit int) ([]Skill, error) {
	if limit <= 0 {
		limit = 5
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, content, source_session_id, built_in, created_at, updated_at,
		       1 - (embedding <=> $1) AS similarity
		FROM skills
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1
		LIMIT $2
	`, formatVector(queryEmbedding), limit)
	if err != nil {
		return nil, fmt.Errorf("searching skills: %w", err)
	}
	defer rows.Close()

	var results []Skill
	for rows.Next() {
		var skill Skill
		var similarity float64
		if err := rows.Scan(
			&skill.ID, &skill.Name, &skill.Description, &skill.Content,
			&skill.SourceSessionID, &skill.BuiltIn, &skill.CreatedAt, &skill.UpdatedAt,
			&similarity,
		); err != nil {
			return nil, fmt.Errorf("scanning skill: %w", err)
		}
		results = append(results, skill)
	}
	return results, rows.Err()
}

// SearchByText generates an embedding for the query text and searches.
func (s *Store) SearchByText(ctx context.Context, query string, limit int) ([]Skill, error) {
	vec, err := s.embeds.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("generating query embedding: %w", err)
	}
	return s.Search(ctx, vec, limit)
}

// Update updates a skill's content and regenerates its embedding.
func (s *Store) Update(ctx context.Context, id, content string) error {
	// Get current skill to regenerate embedding
	skill, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	embedText := skill.Name + ": " + skill.Description
	vec, err := s.embeds.Embed(ctx, embedText)
	if err != nil {
		return fmt.Errorf("generating embedding: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		UPDATE skills SET content = $1, embedding = $2 WHERE id = $3
	`, content, formatVector(vec), id)
	if err != nil {
		return fmt.Errorf("updating skill: %w", err)
	}
	return nil
}

// Delete removes a skill by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM skills WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting skill: %w", err)
	}
	return nil
}

// UpsertBuiltIn inserts a built-in skill or updates it if the name already exists.
// Returns the skill (whether inserted or existing).
func (s *Store) UpsertBuiltIn(ctx context.Context, name, description, content string, embedding Vector) (*Skill, error) {
	var skill Skill
	err := s.pool.QueryRow(ctx, `
		INSERT INTO skills (name, description, content, embedding, built_in)
		VALUES ($1, $2, $3, $4, true)
		ON CONFLICT (name) DO UPDATE SET
			description = EXCLUDED.description,
			content = EXCLUDED.content,
			embedding = EXCLUDED.embedding
		RETURNING id, name, description, content, source_session_id, built_in, created_at, updated_at
	`, name, description, content, formatVector(embedding)).Scan(
		&skill.ID, &skill.Name, &skill.Description, &skill.Content,
		&skill.SourceSessionID, &skill.BuiltIn, &skill.CreatedAt, &skill.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upserting built-in skill %q: %w", name, err)
	}
	return &skill, nil
}

// ListAll returns all skills as summaries.
func (s *Store) ListAll(ctx context.Context) ([]SkillSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, built_in FROM skills ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("listing skills: %w", err)
	}
	defer rows.Close()

	var results []SkillSummary
	for rows.Next() {
		var s SkillSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.BuiltIn); err != nil {
			return nil, fmt.Errorf("scanning skill: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// RecordUsage records that a skill was used in a session.
func (s *Store) RecordUsage(ctx context.Context, skillID, sessionID string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO skill_usage (skill_id, session_id) VALUES ($1, $2) RETURNING id
	`, skillID, sessionID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("recording skill usage: %w", err)
	}
	return id, nil
}

// MarkHelpful updates whether a skill usage was helpful.
func (s *Store) MarkHelpful(ctx context.Context, usageID int64, helpful bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE skill_usage SET was_helpful = $1 WHERE id = $2
	`, helpful, usageID)
	if err != nil {
		return fmt.Errorf("marking skill usage: %w", err)
	}
	return nil
}

// GetUsage retrieves a skill usage record.
func (s *Store) GetUsage(ctx context.Context, usageID int64) (*SkillUsage, error) {
	var su SkillUsage
	err := s.pool.QueryRow(ctx, `
		SELECT id, skill_id, session_id, was_helpful, created_at
		FROM skill_usage WHERE id = $1
	`, usageID).Scan(&su.ID, &su.SkillID, &su.SessionID, &su.WasHelpful, &su.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("getting skill usage %d: %w", usageID, err)
	}
	return &su, nil
}

// Count returns the total number of skills.
func (s *Store) Count(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM skills`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting skills: %w", err)
	}
	return count, nil
}

// formatVector formats a float32 slice as a pgvector-compatible string.
func formatVector(v Vector) string {
	if v == nil {
		return ""
	}
	// pgvector expects format: '[0.1,0.2,0.3]'
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

// parseVector parses a pgvector string into a float32 slice.
func parseVector(s string) (Vector, error) {
	if s == "" || s == "null" {
		return nil, nil
	}
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("invalid vector format: %s", s)
	}
	// Remove brackets
	s = s[1 : len(s)-1]
	if s == "" {
		return Vector{}, nil
	}

	// Split by comma
	var vec Vector
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			var f float32
			_, err := fmt.Sscanf(s[start:i], "%f", &f)
			if err != nil {
				return nil, fmt.Errorf("parsing vector element: %w", err)
			}
			vec = append(vec, f)
			start = i + 1
		}
	}
	return vec, nil
}
