package skills

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/aspectrr/ivy/internal/vine/embed"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Real embedding integration tests.
//
// These tests use a real embedding endpoint and real Postgres pgvector to verify
// that semantic search actually ranks similar content higher.
//
// Gated behind IVY_EMBEDDING_TESTS=1. Requires:
//   - Postgres with pgvector (docker-compose up ivy-postgres, or docker run pgvector/pgvector:pg17)
//   - Ollama with nomic-embed-text (default), or any OpenAI-compatible embedding endpoint
//
// Quick setup:
//
//	ollama pull nomic-embed-text
//	IVY_EMBEDDING_TESTS=1 go test ./internal/vine/skills/ -run TestReal -v
//
// Override endpoint:
//
//	IVY_TEST_EMBED_ENDPOINT=http://my-llm:8080/v1 IVY_TEST_EMBED_MODEL=text-embedding-3-small IVY_TEST_EMBED_DIM=1536

const defaultEmbedEndpoint = "http://localhost:11434/v1"
const defaultEmbedModel = "nomic-embed-text"
const defaultEmbedDim = 768

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEmbedding(t *testing.T) (*embed.Client, int) {
	t.Helper()

	if os.Getenv("IVY_EMBEDDING_TESTS") == "" {
		t.Skip("skipping real embedding tests — set IVY_EMBEDDING_TESTS=1 to run")
	}

	endpoint := envOr("IVY_TEST_EMBED_ENDPOINT", defaultEmbedEndpoint)
	model := envOr("IVY_TEST_EMBED_MODEL", defaultEmbedModel)
	dim := defaultEmbedDim
	if v := os.Getenv("IVY_TEST_EMBED_DIM"); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &dim)
	}

	// Probe endpoint
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint + "/models")
	if err != nil {
		t.Skipf("Embedding endpoint not reachable at %s.\n\n"+
			"Start Ollama and pull the model:\n"+
			"  ollama pull %s\n\n"+
			"Or: docker run -d -p 11434:11434 ollama/ollama && docker exec <id> ollama pull %s\n\n"+
			"Or point to an existing endpoint:\n"+
			"  IVY_TEST_EMBED_ENDPOINT=http://my-llm:8080/v1 IVY_TEST_EMBED_MODEL=text-embedding-3-small IVY_TEST_EMBED_DIM=1536\n\n"+
			"Error: %v", endpoint, model, model, err)
	}
	_ = resp.Body.Close()

	embedClient := embed.NewClient(config.LLMConfig{
		Endpoint:       endpoint,
		APIKey:         "unused",
		EmbeddingModel: model,
	})

	// Smoke test — actually embed something
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vec, err := embedClient.Embed(ctx, "test")
	if err != nil {
		t.Skipf("Embedding endpoint at %s failed for model %q.\n"+
			"Make sure the model is available: ollama pull %s\n\n"+
			"Error: %v", endpoint, model, model, err)
	}

	// Adjust dim if model returns different dimension than expected
	if len(vec) != dim {
		t.Logf("Model %s returns %d-dim vectors (config said %d), using %d", model, len(vec), dim, len(vec))
		dim = len(vec)
	}

	return embedClient, dim
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
		t.Skipf("Postgres not reachable at %s:%d.\n\n"+
			"Start Postgres with pgvector:\n"+
			"  docker run -d --name ivy-postgres -p 5432:5432 \\\n"+
			"    -e POSTGRES_USER=ivy -e POSTGRES_PASSWORD=ivy -e POSTGRES_DB=ivy \\\n"+
			"    pgvector/pgvector:pg17\n\n"+
			"Error: %v", cfg.Host, cfg.Port, err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func setupStore(t *testing.T) (*Store, context.Context, func()) {
	t.Helper()
	embedClient, dim := requireEmbedding(t)
	pool := requirePostgres(t)

	ctx := context.Background()
	if err := database.EnsureEmbeddingDim(ctx, pool, dim); err != nil {
		t.Fatalf("EnsureEmbeddingDim(%d): %v", dim, err)
	}

	store := NewStore(pool, embedClient)
	return store, ctx, func() {}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestReal_EmbedAndSearch(t *testing.T) {
	store, ctx, _ := setupStore(t)

	skills := []struct {
		name, desc, content string
	}{
		{"kafka-lag-" + uuid.New().String()[:8], "How to investigate and fix Kafka consumer group lag when consumers fall behind", "1. Check consumer group lag\n2. Look for GC pauses\n3. Verify partition balance"},
		{"es-mapping-" + uuid.New().String()[:8], "Resolving Elasticsearch field mapping conflicts when log formats change", "1. GET /index/_mapping\n2. Use _analyze API\n3. Reindex with new mapping"},
		{"logstash-grok-" + uuid.New().String()[:8], "Debugging Logstash grok pattern failures and building custom patterns", "1. Use grokdebugger\n2. Start with GREEDYDATA\n3. Test iteratively"},
		{"redpanda-broker-" + uuid.New().String()[:8], "What to do when a Redpanda broker goes offline and partition leadership fails", "1. Check broker health\n2. Verify ISR\n3. Force leadership transfer"},
		{"network-latency-" + uuid.New().String()[:8], "Systematic approach to diagnosing network latency between services", "1. ping/traceroute\n2. Check DNS resolution\n3. Verify firewall rules"},
	}

	ids := make([]string, len(skills))
	for i, s := range skills {
		skill, err := store.Create(ctx, s.name, s.desc, s.content, nil)
		if err != nil {
			t.Fatalf("Create(%s): %v", s.name, err)
		}
		ids[i] = skill.ID
		t.Logf("Created: %s", s.name)
	}
	defer func() {
		for _, id := range ids {
			_ = store.Delete(ctx, id)
		}
	}()

	t.Run("KafkaQueryFindsKafka", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "kafka consumer is lagging behind, messages are piling up", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		assertResultContains(t, results, skills[0].name, "kafka skill should be in top 3")
	})

	t.Run("ESQueryFindsES", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "elasticsearch field type mismatch in log index", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		assertResultContains(t, results, skills[1].name, "ES skill should be in top 3")
	})

	t.Run("LogstashQueryFindsLogstash", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "logstash grok pattern not matching log lines", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		assertResultContains(t, results, skills[2].name, "logstash skill should be in top 3")
	})

	t.Run("NetworkQueryDoesNotRankKafkaFirst", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "why is my network connection slow between two servers", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if results[0].Name == skills[0].name {
			t.Error("kafka skill should NOT be top result for a network query")
		}
	})
}

func TestReal_SearchRanksCorrectly(t *testing.T) {
	store, ctx, _ := setupStore(t)

	topics := []struct {
		name, desc, content string
	}{
		{uuid.New().String()[:8] + "-kafka", "Debugging Apache Kafka consumer group lag when processing falls behind", "Check consumer group status, rebalance partitions, increase poll interval"},
		{uuid.New().String()[:8] + "-es", "Fixing Elasticsearch index mapping conflicts after log format changes", "Use _mapping API, reindex with correct types, update template"},
		{uuid.New().String()[:8] + "-dns", "Resolving DNS resolution failures and slow lookups in production", "Flush DNS cache, check /etc/resolv.conf, verify DNS server health"},
	}

	ids := make([]string, 3)
	for i, tp := range topics {
		s, err := store.Create(ctx, tp.name, tp.desc, tp.content, nil)
		if err != nil {
			t.Fatalf("Create(%s): %v", tp.name, err)
		}
		ids[i] = s.ID
	}
	defer func() {
		for _, id := range ids {
			_ = store.Delete(ctx, id)
		}
	}()

	queries := []struct {
		query string
		want  string
	}{
		{"consumer group is falling behind on Kafka partitions", topics[0].name},
		{"elasticsearch says mapper_parsing_exception", topics[1].name},
		{"DNS lookups are timing out for my services", topics[2].name},
	}

	for _, q := range queries {
		t.Run(fmt.Sprintf("rank_%s", q.want), func(t *testing.T) {
			results, err := store.SearchByText(ctx, q.query, 3)
			if err != nil {
				t.Fatalf("SearchByText(%q): %v", q.query, err)
			}
			if len(results) == 0 {
				t.Fatal("expected results")
			}
			for i, r := range results {
				t.Logf("  #%d: %s", i+1, r.Name)
			}
			if results[0].Name != q.want {
				t.Errorf("top result = %q, want %q", results[0].Name, q.want)
			}
		})
	}
}

func TestReal_UpsertBuiltInWithEmbedding(t *testing.T) {
	embedClient, dim := requireEmbedding(t)
	pool := requirePostgres(t)
	ctx := context.Background()
	if err := database.EnsureEmbeddingDim(ctx, pool, dim); err != nil {
		t.Fatalf("EnsureEmbeddingDim: %v", err)
	}

	vec, err := embedClient.Embed(ctx, "kafka-debugging: Patterns for debugging Kafka consumer lag")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != dim {
		t.Fatalf("expected %d-dim embedding, got %d", dim, len(vec))
	}
	t.Logf("Generated real embedding: %d dimensions", len(vec))

	name := "builtin-" + uuid.New().String()[:8]
	store := NewStore(pool, embedClient)

	skill, err := store.UpsertBuiltIn(ctx, name, "Kafka debugging", "Check consumer groups, GC pauses", vec)
	if err != nil {
		t.Fatalf("UpsertBuiltIn: %v", err)
	}
	if !skill.BuiltIn {
		t.Error("expected built_in = true")
	}

	// Upsert again — should update, not duplicate
	skill2, err := store.UpsertBuiltIn(ctx, name, "Updated Kafka debugging", "Better content", vec)
	if err != nil {
		t.Fatalf("UpsertBuiltIn(2): %v", err)
	}
	if skill2.Description != "Updated Kafka debugging" {
		t.Errorf("description = %q", skill2.Description)
	}

	count := 0
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM skills WHERE name = $1`, name).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row after upsert, got %d", count)
	}
	_ = store.Delete(ctx, skill.ID)
}

func TestReal_EmbeddingBatch(t *testing.T) {
	embedClient, dim := requireEmbedding(t)
	ctx := context.Background()

	texts := []string{
		"Kafka consumer lag investigation",
		"Elasticsearch query optimization",
		"Logstash pipeline configuration",
	}
	vecs, err := embedClient.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != dim {
			t.Errorf("vec[%d] len = %d, want %d", i, len(v), dim)
		}
	}

	// Same text → near-identical embedding
	vecs2, _ := embedClient.EmbedBatch(ctx, texts)
	for i := range vecs {
		sim := cosineSimilarity(vecs[i], vecs2[i])
		if sim < 0.99 {
			t.Errorf("same text should produce near-identical embeddings, similarity[%d] = %.4f", i, sim)
		}
	}

	// Different texts → lower similarity than same texts
	crossSim := cosineSimilarity(vecs[0], vecs[1])
	sameSim := cosineSimilarity(vecs[0], vecs[0])
	t.Logf("Kafka vs ES similarity: %.4f", crossSim)
	t.Logf("Kafka vs Kafka similarity: %.4f", sameSim)
	if crossSim > sameSim {
		t.Error("cross-topic similarity should be lower than same-topic")
	}
}

func TestReal_ListAll(t *testing.T) {
	store, ctx, _ := setupStore(t)

	name := "list-test-" + uuid.New().String()[:8]
	skill, err := store.Create(ctx, name, "List test", "Content", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = store.Delete(ctx, skill.ID) }()

	list, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	found := false
	for _, s := range list {
		if s.ID == skill.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("created skill not found in ListAll")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertResultContains(t *testing.T, results []Skill, name, msg string) {
	t.Helper()
	for _, r := range results {
		t.Logf("  Result: %s", r.Name)
		if r.Name == name {
			return
		}
	}
	t.Error(msg)
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (sqrtF64(normA) * sqrtF64(normB))
}

func sqrtF64(x float64) float64 {
	z := 1.0
	for i := 0; i < 100; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}
