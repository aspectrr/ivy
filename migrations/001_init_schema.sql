-- +goose Up
-- Ivy schema: sessions, events, skills, knowledge_entries, skill_usage

-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------
CREATE TABLE sessions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source      TEXT    NOT NULL,              -- e.g. "clickup", "slack"
    source_id   TEXT    NOT NULL,              -- e.g. ClickUp task ID
    status      TEXT    NOT NULL DEFAULT 'pending',
                    -- pending | running | suspended | completed | failed
    agent_config    JSONB NOT NULL DEFAULT '{}',
    sandbox_id      TEXT,
    metadata        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX sessions_source_idx ON sessions (source, source_id);
CREATE INDEX sessions_status_idx ON sessions (status);
CREATE INDEX sessions_updated_at_idx ON sessions (updated_at);

-- ---------------------------------------------------------------------------
-- Events (append-only)
-- ---------------------------------------------------------------------------
CREATE TABLE events (
    id          BIGSERIAL PRIMARY KEY,
    session_id  UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq         BIGINT NOT NULL,
    type        TEXT NOT NULL,
            -- user_message | agent_message | tool_call | tool_result
            -- interrupt | status_transition | error
    data        JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (session_id, seq)
);

CREATE INDEX events_session_seq_idx ON events (session_id, seq);
CREATE INDEX events_type_idx ON events (session_id, type);
CREATE INDEX events_data_idx ON events USING gin (data);

-- ---------------------------------------------------------------------------
-- Skills
-- ---------------------------------------------------------------------------
CREATE TABLE skills (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT UNIQUE NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    content             TEXT NOT NULL,
    embedding           vector(1536),
    source_session_id   UUID REFERENCES sessions(id) ON DELETE SET NULL,
    built_in            BOOLEAN NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- HNSW index for fast cosine-similarity search
CREATE INDEX skills_embedding_idx ON skills
    USING hnsw (embedding vector_cosine_ops);

-- ---------------------------------------------------------------------------
-- Knowledge entries (indexed session history)
-- ---------------------------------------------------------------------------
CREATE TABLE knowledge_entries (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    content     TEXT NOT NULL,
    embedding   vector(1536),
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX knowledge_entries_embedding_idx ON knowledge_entries
    USING hnsw (embedding vector_cosine_ops);
CREATE INDEX knowledge_entries_session_idx ON knowledge_entries (session_id);

-- ---------------------------------------------------------------------------
-- Skill usage tracking
-- ---------------------------------------------------------------------------
CREATE TABLE skill_usage (
    id          BIGSERIAL PRIMARY KEY,
    skill_id    UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    session_id  UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    was_helpful BOOLEAN,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX skill_usage_skill_idx ON skill_usage (skill_id);
CREATE INDEX skill_usage_session_idx ON skill_usage (session_id);

-- ---------------------------------------------------------------------------
-- Helper: updated_at trigger
-- ---------------------------------------------------------------------------
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ivy_set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER sessions_updated_at
    BEFORE UPDATE ON sessions
    FOR EACH ROW EXECUTE FUNCTION ivy_set_updated_at();

CREATE TRIGGER skills_updated_at
    BEFORE UPDATE ON skills
    FOR EACH ROW EXECUTE FUNCTION ivy_set_updated_at();

-- +goose Down
DROP TRIGGER IF EXISTS skills_updated_at ON skills;
DROP TRIGGER IF EXISTS sessions_updated_at ON sessions;
DROP FUNCTION IF EXISTS ivy_set_updated_at;
DROP TABLE IF EXISTS skill_usage;
DROP TABLE IF EXISTS knowledge_entries;
DROP TABLE IF EXISTS skills;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS sessions;
DROP EXTENSION IF EXISTS vector;
