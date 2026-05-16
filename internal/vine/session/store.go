package session

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aspectrr/ivy/internal/vine/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides CRUD operations for sessions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a new session store backed by the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new session and returns it with the generated ID and timestamps.
func (s *Store) Create(ctx context.Context, source, sourceID string, agentConfig json.RawMessage) (*model.Session, error) {
	if agentConfig == nil {
		agentConfig = json.RawMessage(`{}`)
	}

	var sess model.Session
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (source, source_id, agent_config)
		VALUES ($1, $2, $3)
		RETURNING id, source, source_id, status, agent_config, sandbox_id, metadata, created_at, updated_at
	`, source, sourceID, agentConfig).Scan(
		&sess.ID, &sess.Source, &sess.SourceID, &sess.Status,
		&sess.AgentConfig, &sess.SandboxID, &sess.Metadata,
		&sess.CreatedAt, &sess.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting session: %w", err)
	}
	return &sess, nil
}

// Get retrieves a session by ID.
func (s *Store) Get(ctx context.Context, id string) (*model.Session, error) {
	var sess model.Session
	err := s.pool.QueryRow(ctx, `
		SELECT id, source, source_id, status, agent_config, sandbox_id, metadata, created_at, updated_at
		FROM sessions WHERE id = $1
	`, id).Scan(
		&sess.ID, &sess.Source, &sess.SourceID, &sess.Status,
		&sess.AgentConfig, &sess.SandboxID, &sess.Metadata,
		&sess.CreatedAt, &sess.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("session %s: not found", id)
		}
		return nil, fmt.Errorf("querying session: %w", err)
	}
	return &sess, nil
}

// GetBySource retrieves a session by its source and source_id (e.g. clickup + task ID).
func (s *Store) GetBySource(ctx context.Context, source, sourceID string) (*model.Session, error) {
	var sess model.Session
	err := s.pool.QueryRow(ctx, `
		SELECT id, source, source_id, status, agent_config, sandbox_id, metadata, created_at, updated_at
		FROM sessions WHERE source = $1 AND source_id = $2
	`, source, sourceID).Scan(
		&sess.ID, &sess.Source, &sess.SourceID, &sess.Status,
		&sess.AgentConfig, &sess.SandboxID, &sess.Metadata,
		&sess.CreatedAt, &sess.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("session %s/%s: not found", source, sourceID)
		}
		return nil, fmt.Errorf("querying session by source: %w", err)
	}
	return &sess, nil
}

// UpdateStatus transitions a session's status.
func (s *Store) UpdateStatus(ctx context.Context, id string, status string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET status = $1 WHERE id = $2
	`, status, id)
	if err != nil {
		return fmt.Errorf("updating session status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session %s: not found", id)
	}
	return nil
}

// SetSandboxID associates a sandbox container with the session.
func (s *Store) SetSandboxID(ctx context.Context, id string, sandboxID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET sandbox_id = $1 WHERE id = $2
	`, sandboxID, id)
	if err != nil {
		return fmt.Errorf("setting sandbox id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session %s: not found", id)
	}
	return nil
}

// ClearSandboxID removes the sandbox association from a session.
func (s *Store) ClearSandboxID(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET sandbox_id = NULL WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("clearing sandbox id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session %s: not found", id)
	}
	return nil
}

// ListByStatus returns sessions with the given status, ordered by updated_at desc.
// Use limit and offset for pagination.
func (s *Store) ListByStatus(ctx context.Context, status string, limit, offset int) ([]model.Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, source, source_id, status, agent_config, sandbox_id, metadata, created_at, updated_at
		FROM sessions
		WHERE status = $1
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3
	`, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing sessions by status: %w", err)
	}
	defer rows.Close()

	var sessions []model.Session
	for rows.Next() {
		var sess model.Session
		if err := rows.Scan(
			&sess.ID, &sess.Source, &sess.SourceID, &sess.Status,
			&sess.AgentConfig, &sess.SandboxID, &sess.Metadata,
			&sess.CreatedAt, &sess.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sessions: %w", err)
	}
	return sessions, nil
}

// UpdateMetadata merges the given metadata into the session's existing metadata.
func (s *Store) UpdateMetadata(ctx context.Context, id string, metadata json.RawMessage) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET metadata = metadata || $1 WHERE id = $2
	`, metadata, id)
	if err != nil {
		return fmt.Errorf("updating session metadata: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session %s: not found", id)
	}
	return nil
}
