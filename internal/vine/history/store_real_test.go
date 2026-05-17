package history

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/aspectrr/ivy/internal/vine/embed"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Real embedding integration tests for history search.
//
// Gated behind IVY_EMBEDDING_TESTS=1. Same requirements as skills real tests:
// Postgres + Ollama with nomic-embed-text (or override via env vars).
//
//	IVY_EMBEDDING_TESTS=1 go test ./internal/vine/history/ -run TestReal -v

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requirePostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := config.DatabaseConfig{
		Host:     envOr("IVY_TEST_DB_HOST", "localhost"),
		Port:     5432,
		Name:     "ivy",
		User:     "ivy",
		Password: envOr("IVY_TEST_DB_PASSWORD", "ivy"),
		SSLMode:  "disable",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		t.Skipf("Postgres not reachable.\n\n"+
			"Start Postgres with pgvector:\n"+
			"  docker run -d --name ivy-postgres -p 5432:5432 \\\n"+
			"    -e POSTGRES_USER=ivy -e POSTGRES_PASSWORD=ivy -e POSTGRES_DB=ivy \\\n"+
			"    pgvector/pgvector:pg17\n\n"+
			"Error: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func requireEmbedding(t *testing.T) (*embed.Client, int) {
	t.Helper()
	if os.Getenv("IVY_EMBEDDING_TESTS") == "" {
		t.Skip("skipping real embedding tests — set IVY_EMBEDDING_TESTS=1 to run")
	}

	endpoint := envOr("IVY_TEST_EMBED_ENDPOINT", "http://localhost:11434/v1")
	model := envOr("IVY_TEST_EMBED_MODEL", "nomic-embed-text")
	dim := 768
	if v := os.Getenv("IVY_TEST_EMBED_DIM"); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &dim)
	}

	client := embed.NewClient(config.LLMConfig{
		Endpoint:       endpoint,
		APIKey:         "unused",
		EmbeddingModel: model,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vec, err := client.Embed(ctx, "test")
	if err != nil {
		t.Skipf("Embedding endpoint not reachable at %s (model %s).\n"+
			"Start Ollama: ollama pull %s\n\n"+
			"Error: %v", endpoint, model, model, err)
	}
	if len(vec) != dim {
		dim = len(vec)
	}
	return client, dim
}

func makeSession(t *testing.T, pool *pgxpool.Pool, source string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO sessions (source, source_id, status) VALUES ($1, $2, 'completed') RETURNING id
	`, source, fmt.Sprintf("real-test-%s", uuid.New().String())).Scan(&id)
	if err != nil {
		t.Fatalf("creating test session: %v", err)
	}
	return id
}

func setupStore(t *testing.T) (*Store, context.Context, *pgxpool.Pool) {
	t.Helper()
	embedClient, dim := requireEmbedding(t)
	pool := requirePostgres(t)
	ctx := context.Background()
	if err := database.EnsureEmbeddingDim(ctx, pool, dim); err != nil {
		t.Fatalf("EnsureEmbeddingDim(%d): %v", dim, err)
	}
	return NewStore(pool, embedClient), ctx, pool
}

func TestReal_IndexAndSearch(t *testing.T) {
	store, ctx, pool := setupStore(t)

	sessions := []struct {
		summary string
		source  string
	}{
		{"Fixed Kafka consumer lag by increasing max.poll.interval.ms from 5m to 15m and rebalancing consumer groups", "clickup"},
		{"Resolved Elasticsearch mapping conflict by creating a new index with explicit mappings and reindexing data", "clickup"},
		{"Debugged Logstash pipeline that was dropping events due to grok pattern failures on malformed JSON logs", "manual"},
		{"Investigated DNS resolution timeouts caused by misconfigured /etc/resolv.conf pointing to dead DNS server", "clickup"},
		{"Fixed disk space issue on Redpanda broker by cleaning old segments and increasing segment deletion threshold", "manual"},
	}

	sessionIDs := make([]string, len(sessions))
	entryIDs := make([]string, len(sessions))
	for i, s := range sessions {
		sessionIDs[i] = makeSession(t, pool, s.source)
		entry, err := store.IndexSession(ctx, sessionIDs[i], s.summary)
		if err != nil {
			t.Fatalf("IndexSession(%d): %v", i, err)
		}
		entryIDs[i] = entry.ID
		t.Logf("Indexed: %s... → session %s", s.summary[:50], sessionIDs[i][:8])
	}
	defer func() {
		for _, id := range entryIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE id = $1`, id)
		}
		for _, id := range sessionIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
		}
	}()

	t.Run("KafkaQueryFindsKafka", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "kafka consumers are lagging, messages piling up", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		for i, r := range results {
			t.Logf("  #%d: %s", i+1, trunc(r.Content, 60))
		}
		if results[0].SessionID != sessionIDs[0] {
			t.Errorf("top result = session %s, want kafka session %s", results[0].SessionID[:8], sessionIDs[0][:8])
		}
	})

	t.Run("ESQueryFindsES", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "elasticsearch field type conflict in index mapping", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		for i, r := range results {
			t.Logf("  #%d: %s", i+1, trunc(r.Content, 60))
		}
		if results[0].SessionID != sessionIDs[1] {
			t.Errorf("top result = session %s, want ES session %s", results[0].SessionID[:8], sessionIDs[1][:8])
		}
	})

	t.Run("LogstashQueryFindsLogstash", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "logstash dropping events, grok parse failures", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		for i, r := range results {
			t.Logf("  #%d: %s", i+1, trunc(r.Content, 60))
		}
		if results[0].SessionID != sessionIDs[2] {
			t.Errorf("top result = session %s, want logstash session %s", results[0].SessionID[:8], sessionIDs[2][:8])
		}
	})

	t.Run("DNSQueryFindsDNS", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "DNS lookups timing out, name resolution failing", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		for i, r := range results {
			t.Logf("  #%d: %s", i+1, trunc(r.Content, 60))
		}
		if results[0].SessionID != sessionIDs[3] {
			t.Errorf("top result = session %s, want DNS session %s", results[0].SessionID[:8], sessionIDs[3][:8])
		}
	})

	t.Run("FilterBySource", func(t *testing.T) {
		results, err := store.SearchByFilter(ctx, map[string]interface{}{"source": "manual"}, 10, 0)
		if err != nil {
			t.Fatalf("SearchByFilter: %v", err)
		}
		for _, r := range results {
			if r.Source != "manual" {
				t.Errorf("expected source=manual, got %s", r.Source)
			}
		}
		if len(results) < 2 {
			t.Errorf("expected at least 2 manual sessions, got %d", len(results))
		}
	})
}

func TestReal_SearchRanksCorrectly(t *testing.T) {
	store, ctx, pool := setupStore(t)

	summaries := []string{
		"Fixed Kafka consumer group lag by adjusting max.poll.interval.ms",
		"Resolved Elasticsearch mapping conflict by reindexing with explicit types",
		"Fixed DNS resolution failures by updating /etc/resolv.conf",
	}

	sessionIDs := make([]string, 3)
	for i, s := range summaries {
		sessionIDs[i] = makeSession(t, pool, "test")
		_, err := store.IndexSession(ctx, sessionIDs[i], s)
		if err != nil {
			t.Fatalf("IndexSession(%d): %v", i, err)
		}
	}
	defer func() {
		for _, id := range sessionIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE session_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
		}
	}()

	queries := []struct {
		query string
		want  int // index into sessionIDs
	}{
		{"kafka consumers falling behind on message processing", 0},
		{"elasticsearch mapper_parsing_exception in log index", 1},
		{"DNS name resolution timing out intermittently", 2},
	}

	for _, q := range queries {
		t.Run(fmt.Sprintf("rank_%d", q.want), func(t *testing.T) {
			results, err := store.SearchByText(ctx, q.query, 3)
			if err != nil {
				t.Fatalf("SearchByText(%q): %v", q.query, err)
			}
			if len(results) == 0 {
				t.Fatal("expected results")
			}
			for i, r := range results {
				t.Logf("  #%d: session=%s", i+1, r.SessionID[:8])
			}
			if results[0].SessionID != sessionIDs[q.want] {
				t.Errorf("top result = %s, want session[%d] = %s",
					results[0].SessionID[:8], q.want, sessionIDs[q.want][:8])
			}
		})
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
