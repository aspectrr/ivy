package database

import (
	"context"
	"fmt"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a new pgxpool.Pool from the given database config.
func NewPool(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parsing pool config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}

// EnsureEmbeddingDim checks that the vector column dimensions match the
// configured embedding_dim and alters them if needed. This is a startup
// operation — it drops and recreates HNSW indexes.
func EnsureEmbeddingDim(ctx context.Context, pool *pgxpool.Pool, dim int) error {
	if dim <= 0 {
		return nil // use whatever the schema has
	}

	// Check current dimension of the skills.embedding column
	var currentDim int
	err := pool.QueryRow(ctx, `
		SELECT a.atttypmod
		FROM pg_attribute a
		JOIN pg_class c ON a.attrelid = c.oid
		JOIN pg_namespace n ON c.relnamespace = n.oid
		WHERE c.relname = 'skills' AND a.attname = 'embedding' AND n.nspname = 'public'
	`).Scan(&currentDim)
	if err != nil {
		return fmt.Errorf("checking vector dimension: %w", err)
	}

	// pgvector stores dim as atttypmod where actual_dim = atttypmod - 4
	// (varlena overhead). If atttypmod is -1, the column type isn't set.
	if currentDim != -1 {
		actualDim := currentDim - 4
		if actualDim == dim {
			return nil // already correct
		}
	}

	// Drop indexes that depend on vector columns
	_, _ = pool.Exec(ctx, `DROP INDEX IF EXISTS skills_embedding_idx`)
	_, _ = pool.Exec(ctx, `DROP INDEX IF EXISTS knowledge_entries_embedding_idx`)

	// Null out existing embeddings (can't cast between dimensions)
	_, _ = pool.Exec(ctx, `UPDATE skills SET embedding = NULL WHERE embedding IS NOT NULL`)
	_, _ = pool.Exec(ctx, `UPDATE knowledge_entries SET embedding = NULL WHERE embedding IS NOT NULL`)

	// Alter columns
	alterSQL := fmt.Sprintf(`
		ALTER TABLE skills ALTER COLUMN embedding TYPE vector(%d) USING NULL;
		ALTER TABLE knowledge_entries ALTER COLUMN embedding TYPE vector(%d) USING NULL;
	`, dim, dim)
	if _, err := pool.Exec(ctx, alterSQL); err != nil {
		return fmt.Errorf("altering embedding dimensions to %d: %w", dim, err)
	}

	// Recreate indexes
	indexSQL := `
		CREATE INDEX skills_embedding_idx ON skills USING hnsw (embedding vector_cosine_ops);
		CREATE INDEX knowledge_entries_embedding_idx ON knowledge_entries USING hnsw (embedding vector_cosine_ops);
	`
	if _, err := pool.Exec(ctx, indexSQL); err != nil {
		return fmt.Errorf("recreating embedding indexes: %w", err)
	}

	return nil
}
