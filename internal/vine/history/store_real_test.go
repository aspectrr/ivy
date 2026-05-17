package history

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/aspectrr/ivy/internal/vine/embed"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests use REAL Ollama embeddings and REAL Postgres pgvector.
// Run with: IVY_EMBEDDING_TESTS=1 go test ./internal/vine/history/ -run TestReal -v

func realPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := config.DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		Name:     "ivy",
		User:     "ivy",
		Password: "ivy",
		SSLMode:  "disable",
	}
	pool, err := database.NewPool(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connecting to database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func realEmbedClient(t *testing.T) *embed.Client {
	t.Helper()
	return embed.NewClient(config.LLMConfig{
		Endpoint:       "http://localhost:11434/v1",
		APIKey:         "ollama",
		EmbeddingModel: "nomic-embed-text",
	})
}

func ensureDim(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := database.EnsureEmbeddingDim(context.Background(), pool, 768); err != nil {
		t.Fatalf("EnsureEmbeddingDim(768): %v", err)
	}
}

func realCreateTestSession(t *testing.T, pool *pgxpool.Pool, source string) string {
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

func TestReal_IndexAndSearch(t *testing.T) {
	if os.Getenv("IVY_EMBEDDING_TESTS") == "" {
		t.Skip("skipping real embedding test (set IVY_EMBEDDING_TESTS=1)")
	}

	pool := realPool(t)
	ensureDim(t, pool)
	embedClient := realEmbedClient(t)
	store := NewStore(pool, embedClient)
	ctx := context.Background()

	// Index sessions with distinct summaries
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
		sessionIDs[i] = realCreateTestSession(t, pool, s.source)
		entry, err := store.IndexSession(ctx, sessionIDs[i], s.summary)
		if err != nil {
			t.Fatalf("IndexSession(%d): %v", i, err)
		}
		entryIDs[i] = entry.ID
		t.Logf("Indexed session %s: %s...", sessionIDs[i][:8], s.summary[:50])
	}

	// Test: search for Kafka issues
	t.Run("KafkaSearch", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "kafka consumers are lagging, messages piling up", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		for i, r := range results {
			t.Logf("  #%d: session=%s content=%q", i+1, r.SessionID[:8], truncate(r.Content, 60))
		}
		// Kafka session should be in top results
		if results[0].SessionID != sessionIDs[0] {
			t.Errorf("top result session = %s, want %s (kafka)", results[0].SessionID[:8], sessionIDs[0][:8])
		}
	})

	// Test: search for ES issues
	t.Run("ElasticsearchSearch", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "elasticsearch field type conflict in index mapping", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		for i, r := range results {
			t.Logf("  #%d: %q", i+1, truncate(r.Content, 60))
		}
		if results[0].SessionID != sessionIDs[1] {
			t.Errorf("top result = %s, want ES session %s", results[0].SessionID[:8], sessionIDs[1][:8])
		}
	})

	// Test: search for Logstash issues
	t.Run("LogstashSearch", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "logstash dropping events, grok parse failures", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		for i, r := range results {
			t.Logf("  #%d: %q", i+1, truncate(r.Content, 60))
		}
		if results[0].SessionID != sessionIDs[2] {
			t.Errorf("top result = %s, want Logstash session %s", results[0].SessionID[:8], sessionIDs[2][:8])
		}
	})

	// Test: search for DNS issues — should NOT return Kafka as #1
	t.Run("DNSSearch", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "DNS lookups timing out, name resolution failing", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		for i, r := range results {
			t.Logf("  #%d: %q", i+1, truncate(r.Content, 60))
		}
		if results[0].SessionID != sessionIDs[3] {
			t.Errorf("top result = %s, want DNS session %s", results[0].SessionID[:8], sessionIDs[3][:8])
		}
	})

	// Test: structured filter by source
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

	// Cleanup
	for _, id := range entryIDs {
		_, _ = pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE id = $1`, id)
	}
	for _, id := range sessionIDs {
		_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	}
}

func TestReal_SearchRanksCorrectly(t *testing.T) {
	if os.Getenv("IVY_EMBEDDING_TESTS") == "" {
		t.Skip("skipping real embedding test (set IVY_EMBEDDING_TESTS=1)")
	}

	pool := realPool(t)
	ensureDim(t, pool)
	embedClient := realEmbedClient(t)
	store := NewStore(pool, embedClient)
	ctx := context.Background()

	// Index 3 sessions with very different topics
	summaries := []string{
		"Fixed Kafka consumer group lag by adjusting max.poll.interval.ms",
		"Resolved Elasticsearch mapping conflict by reindexing with explicit types",
		"Fixed DNS resolution failures by updating /etc/resolv.conf",
	}

	sessionIDs := make([]string, 3)
	for i, s := range summaries {
		sessionIDs[i] = realCreateTestSession(t, pool, "test")
		_, err := store.IndexSession(ctx, sessionIDs[i], s)
		if err != nil {
			t.Fatalf("IndexSession(%d): %v", i, err)
		}
	}

	// Each query should rank its corresponding topic as #1
	queries := []struct {
		query       string
		wantSession int
	}{
		{"kafka consumers falling behind on message processing", 0},
		{"elasticsearch mapper_parsing_exception in log index", 1},
		{"DNS name resolution timing out intermittently", 2},
	}

	for _, q := range queries {
		t.Run(fmt.Sprintf("rank_%d", q.wantSession), func(t *testing.T) {
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
			if results[0].SessionID != sessionIDs[q.wantSession] {
				t.Errorf("top result = %s, want session[%d] = %s",
					results[0].SessionID[:8], q.wantSession, sessionIDs[q.wantSession][:8])
			}
		})
	}

	// Cleanup
	for _, id := range sessionIDs {
		_, _ = pool.Exec(ctx, `DELETE FROM knowledge_entries WHERE session_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
