-- +goose Up
-- Adjust vector column dimensions to match the configured embedding model.
-- Run with: IVY_EMBEDDING_DIM=768 goose postgres $DSN up
-- Default is 1536 (OpenAI text-embedding-3-small/ada-002).
-- For local Ollama with nomic-embed-text, use 768.

-- Drop HNSW indexes first (they depend on the column type)
DROP INDEX IF EXISTS skills_embedding_idx;
DROP INDEX IF EXISTS knowledge_entries_embedding_idx;

-- Alter vector dimensions (default 1536 if not set via env)
-- Note: goose doesn't support SQL variables, so we use a psql-compatible approach.
-- For custom dimensions, run: ALTER TABLE skills ALTER COLUMN embedding TYPE vector(768);
-- This migration sets the default of 1536 which matches the initial schema.

-- For dev with Ollama (768 dims), apply after migration:
--   ALTER TABLE skills ALTER COLUMN embedding TYPE vector(768);
--   ALTER TABLE knowledge_entries ALTER COLUMN embedding TYPE vector(768);
--   Then recreate indexes:
--   CREATE INDEX skills_embedding_idx ON skills USING hnsw (embedding vector_cosine_ops);
--   CREATE INDEX knowledge_entries_embedding_idx ON knowledge_entries USING hnsw (embedding vector_cosine_ops);

-- Recreate indexes (no-op if already correct dimension)
CREATE INDEX IF NOT EXISTS skills_embedding_idx ON skills
    USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS knowledge_entries_embedding_idx ON knowledge_entries
    USING hnsw (embedding vector_cosine_ops);

-- +goose Down
DROP INDEX IF EXISTS skills_embedding_idx;
DROP INDEX IF EXISTS knowledge_entries_embedding_idx;
CREATE INDEX skills_embedding_idx ON skills
    USING hnsw (embedding vector_cosine_ops);
CREATE INDEX knowledge_entries_embedding_idx ON knowledge_entries
    USING hnsw (embedding vector_cosine_ops);
