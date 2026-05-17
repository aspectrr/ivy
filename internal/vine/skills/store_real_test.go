package skills

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
// They require:
//   - Postgres running (docker-compose up ivy-postgres)
//   - Ollama running with nomic-embed-text model pulled
//
// Run with: IVY_EMBEDDING_TESTS=1 go test ./internal/vine/skills/ -run TestReal -v

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
	client := embed.NewClient(config.LLMConfig{
		Endpoint:       "http://localhost:11434/v1",
		APIKey:         "ollama",
		EmbeddingModel: "nomic-embed-text",
	})
	return client
}

func ensureDim(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if err := database.EnsureEmbeddingDim(ctx, pool, 768); err != nil {
		t.Fatalf("EnsureEmbeddingDim(768): %v", err)
	}
}

func TestReal_EmbedAndSearch(t *testing.T) {
	if os.Getenv("IVY_EMBEDDING_TESTS") == "" {
		t.Skip("skipping real embedding test (set IVY_EMBEDDING_TESTS=1)")
	}

	pool := realPool(t)
	ensureDim(t, pool)
	embedClient := realEmbedClient(t)
	store := NewStore(pool, embedClient)
	ctx := context.Background()

	// Create 5 skills with distinct topics
	skills := []struct {
		name    string
		desc    string
		content string
	}{
		{"kafka-consumer-lag-" + uuid.New().String()[:8], "How to investigate and fix Kafka consumer group lag when consumers fall behind", "1. Check consumer group lag\n2. Look for GC pauses\n3. Verify partition balance"},
		{"elasticsearch-mapping-conflict-" + uuid.New().String()[:8], "Resolving Elasticsearch field mapping conflicts when log formats change", "1. GET /index/_mapping\n2. Use _analyze API\n3. Reindex with new mapping"},
		{"logstash-grok-debugging-" + uuid.New().String()[:8], "Debugging Logstash grok pattern failures and building custom patterns", "1. Use grokdebugger\n2. Start with GREEDYDATA\n3. Test iteratively"},
		{"redpanda-broker-down-" + uuid.New().String()[:8], "What to do when a Redpanda broker goes offline and partition leadership fails", "1. Check broker health\n2. Verify ISR\n3. Force leadership transfer"},
		{"network-latency-debugging-" + uuid.New().String()[:8], "Systematic approach to diagnosing network latency between services", "1. ping/traceroute\n2. Check DNS resolution\n3. Verify firewall rules"},
	}

	createdNames := make(map[string]string) // name → ID
	for _, s := range skills {
		skill, err := store.Create(ctx, s.name, s.desc, s.content, nil)
		if err != nil {
			t.Fatalf("Create(%s): %v", s.name, err)
		}
		createdNames[s.name] = skill.ID
		t.Logf("Created skill: %s (id=%s)", s.name, skill.ID)
	}

	// Test: search for Kafka-related content
	t.Run("KafkaSearch", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "kafka consumer is lagging behind, messages are piling up", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results for kafka search")
		}

		// The kafka skill should rank in the top results
		found := false
		for _, r := range results {
			t.Logf("  Result: %s", r.Name)
			if r.Name == skills[0].name {
				found = true
			}
		}
		if !found {
			t.Errorf("kafka-consumer-lag skill not in top 3 results; got: %v", results[0].Name)
		}
	})

	// Test: search for Elasticsearch-related content
	t.Run("ElasticsearchSearch", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "elasticsearch field type mismatch in log index", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results for ES search")
		}

		found := false
		for _, r := range results {
			t.Logf("  Result: %s", r.Name)
			if r.Name == skills[1].name {
				found = true
			}
		}
		if !found {
			t.Errorf("elasticsearch-mapping skill not in top 3 results; got: %v", results[0].Name)
		}
	})

	// Test: search for Logstash grok issues
	t.Run("LogstashSearch", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "logstash grok pattern not matching log lines", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results for logstash search")
		}

		found := false
		for _, r := range results {
			t.Logf("  Result: %s", r.Name)
			if r.Name == skills[2].name {
				found = true
			}
		}
		if !found {
			t.Errorf("logstash-grok skill not in top 3 results; got: %v", results[0].Name)
		}
	})

	// Test: semantically distinct query should NOT return kafka as top result
	t.Run("NetworkQueryNotKafka", func(t *testing.T) {
		results, err := store.SearchByText(ctx, "why is my network connection slow between two servers", 3)
		if err != nil {
			t.Fatalf("SearchByText: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results for network search")
		}

		// Top result should be network-latency-debugging, NOT kafka
		for _, r := range results {
			t.Logf("  Result: %s", r.Name)
		}
		if results[0].Name == skills[0].name {
			t.Error("kafka skill should NOT be top result for network query")
		}
	})

	// Cleanup
	for _, id := range createdNames {
		_ = store.Delete(ctx, id)
	}
}

func TestReal_UpsertBuiltInWithEmbedding(t *testing.T) {
	if os.Getenv("IVY_EMBEDDING_TESTS") == "" {
		t.Skip("skipping real embedding test (set IVY_EMBEDDING_TESTS=1)")
	}

	pool := realPool(t)
	ensureDim(t, pool)
	embedClient := realEmbedClient(t)
	ctx := context.Background()

	// Generate a real embedding for a built-in skill
	vec, err := embedClient.Embed(ctx, "kafka-debugging: Patterns for debugging Kafka consumer lag and partition rebalancing")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	t.Logf("Generated embedding with %d dimensions", len(vec))

	if len(vec) != 768 {
		t.Fatalf("expected 768-dim embedding, got %d", len(vec))
	}

	name := "builtin-kafka-" + uuid.New().String()[:8]
	store := NewStore(pool, embedClient)

	skill, err := store.UpsertBuiltIn(ctx, name, "Kafka debugging patterns", "Check consumer groups, look for GC pauses", vec)
	if err != nil {
		t.Fatalf("UpsertBuiltIn: %v", err)
	}
	if !skill.BuiltIn {
		t.Error("expected built_in = true")
	}

	// Upsert again — should update
	skill2, err := store.UpsertBuiltIn(ctx, name, "Updated Kafka debugging patterns", "Check consumer groups, ISR count, GC pauses", vec)
	if err != nil {
		t.Fatalf("UpsertBuiltIn(2): %v", err)
	}
	if skill2.Description != "Updated Kafka debugging patterns" {
		t.Errorf("description = %q", skill2.Description)
	}

	// Verify only one row
	count := 0
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM skills WHERE name = $1`, name).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	_ = store.Delete(ctx, skill.ID)
}

func TestReal_EmbeddingBatch(t *testing.T) {
	if os.Getenv("IVY_EMBEDDING_TESTS") == "" {
		t.Skip("skipping real embedding test (set IVY_EMBEDDING_TESTS=1)")
	}

	embedClient := realEmbedClient(t)
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
		if len(v) != 768 {
			t.Errorf("vec[%d] len = %d, want 768", i, len(v))
		}
	}

	// Embeddings for semantically different texts should have low similarity
	// (higher cosine distance). Embeddings for the same text should be identical.
	vecs2, _ := embedClient.EmbedBatch(ctx, texts)
	for i := range vecs {
		sim := cosineSimilarity(vecs[i], vecs2[i])
		if sim < 0.99 {
			t.Errorf("same text should produce identical embeddings, similarity[%d] = %.4f", i, sim)
		}
	}

	// Different texts should have lower similarity than same texts
	crossSim := cosineSimilarity(vecs[0], vecs[1])
	sameSim := cosineSimilarity(vecs[0], vecs[0])
	t.Logf("Kafka vs ES similarity: %.4f", crossSim)
	t.Logf("Kafka vs Kafka similarity: %.4f", sameSim)
	if crossSim > sameSim {
		t.Error("cross-topic similarity should be lower than same-topic")
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

	// Create 3 skills with very different topics
	names := []string{
		uuid.New().String()[:8] + "-kafka-lag",
		uuid.New().String()[:8] + "-es-mapping",
		uuid.New().String()[:8] + "-dns-lookup",
	}
	descs := []string{
		"Debugging Apache Kafka consumer group lag when processing falls behind",
		"Fixing Elasticsearch index mapping conflicts after log format changes",
		"Resolving DNS resolution failures and slow lookups in production",
	}
	contents := []string{
		"Check consumer group status, rebalance partitions, increase poll interval",
		"Use _mapping API, reindex with correct types, update template",
		"Flush DNS cache, check /etc/resolv.conf, verify DNS server health",
	}

	for i := range names {
		_, err := store.Create(ctx, names[i], descs[i], contents[i], nil)
		if err != nil {
			t.Fatalf("Create(%s): %v", names[i], err)
		}
	}

	// Search for each topic and verify the correct skill ranks #1
	queries := []struct {
		query         string
		expectedFirst string
	}{
		{"consumer group is falling behind on Kafka partitions", names[0]},
		{"elasticsearch says mapper_parsing_exception", names[1]},
		{"DNS lookups are timing out for my services", names[2]},
	}

	for _, q := range queries {
		t.Run(fmt.Sprintf("query_%s", q.expectedFirst[:16]), func(t *testing.T) {
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
			if results[0].Name != q.expectedFirst {
				t.Errorf("top result = %q, want %q", results[0].Name, q.expectedFirst)
			}
		})
	}

	// Cleanup
	for _, n := range names {
		s, _ := store.GetByName(ctx, n)
		if s != nil {
			_ = store.Delete(ctx, s.ID)
		}
	}
}

func TestReal_ListAll(t *testing.T) {
	if os.Getenv("IVY_EMBEDDING_TESTS") == "" {
		t.Skip("skipping real embedding test (set IVY_EMBEDDING_TESTS=1)")
	}

	pool := realPool(t)
	ensureDim(t, pool)
	embedClient := realEmbedClient(t)
	store := NewStore(pool, embedClient)
	ctx := context.Background()

	name := "list-test-" + uuid.New().String()[:8]
	skill, err := store.Create(ctx, name, "List test", "Content", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

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

	_ = store.Delete(ctx, skill.ID)
}

// cosineSimilarity computes the cosine similarity between two vectors.
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
	return dot / (sqrt(normA) * sqrt(normB))
}

//go:nosplit
func sqrt(x float64) float64 {
	// Newton's method
	z := 1.0
	for i := 0; i < 100; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}
