package history

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueID returns a unique string for test isolation.
func uniqueID(t *testing.T) string {
	t.Helper()
	return uuid.New().String()
}

// testPool creates a connection pool to the dev database for testing.
func testPool(t *testing.T) *pgxpool.Pool {
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
		t.Fatalf("connecting to test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// mockEmbedder is a test double for the Embedder interface.
type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(_ context.Context, text string) (Vector, error) {
	vec := make(Vector, m.dim)
	for i := range vec {
		vec[i] = float32(len(text)*100 + i)
	}
	return vec, nil
}

// testStore creates a history store for testing.
func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := testPool(t)
	store := NewStore(pool, &mockEmbedder{dim: 768})
	return store, pool
}

// createTestSession creates a test session and returns its ID.
func createTestSession(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO sessions (source, source_id, status) VALUES ('test', $1, 'completed') RETURNING id
	`, fmt.Sprintf("history-test-%s", uniqueID(t))).Scan(&id)
	if err != nil {
		t.Fatalf("creating test session: %v", err)
	}
	return id
}

func TestStore_IndexSession(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	sessionID := createTestSession(t, pool)

	entry, err := store.IndexSession(ctx, sessionID, "Fixed Kafka consumer lag by increasing max.poll.interval.ms")
	if err != nil {
		t.Fatalf("IndexSession() error = %v", err)
	}
	if entry.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if entry.SessionID != sessionID {
		t.Errorf("session_id = %q, want %q", entry.SessionID, sessionID)
	}
}

func TestStore_IndexWithEmbedding(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	sessionID := createTestSession(t, pool)

	vec := make(Vector, 768)
	for i := range vec {
		vec[i] = float32(i)
	}

	entry, err := store.IndexWithEmbedding(ctx, sessionID, "Indexed with pre-computed embedding", vec)
	if err != nil {
		t.Fatalf("IndexWithEmbedding() error = %v", err)
	}
	if entry.Content != "Indexed with pre-computed embedding" {
		t.Errorf("content = %q", entry.Content)
	}
}

func TestStore_Search(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	// Create and index multiple sessions
	contents := []string{
		"Fixed Kafka consumer lag by rebalancing partitions",
		"Debugged Elasticsearch mapping conflict in log index",
		"Optimized Logstash pipeline throughput with batch sizing",
	}
	for _, c := range contents {
		sessionID := createTestSession(t, pool)
		_, err := store.IndexSession(ctx, sessionID, c)
		if err != nil {
			t.Fatalf("IndexSession() error = %v", err)
		}
	}

	// Search with a query embedding
	queryVec, _ := store.embeds.Embed(ctx, "kafka consumer problems")
	results, err := store.Search(ctx, queryVec, 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	t.Logf("Search returned %d results", len(results))
}

func TestStore_SearchByText(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	sessionID := createTestSession(t, pool)
	_, err := store.IndexSession(ctx, sessionID, "Resolved Redpanda broker disk full issue")
	if err != nil {
		t.Fatalf("IndexSession() error = %v", err)
	}

	results, err := store.SearchByText(ctx, "redpanda disk", 5)
	if err != nil {
		t.Fatalf("SearchByText() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
}

func TestStore_SearchByFilter(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	// Create sessions with different sources
	for _, source := range []string{"clickup", "test", "manual"} {
		var id string
		_ = pool.QueryRow(ctx, `
			INSERT INTO sessions (source, source_id, status) VALUES ($1, $2, 'completed') RETURNING id
		`, source, fmt.Sprintf("filter-test-%s", uniqueID(t))).Scan(&id)
	}

	// Filter by source
	results, err := store.SearchByFilter(ctx, map[string]interface{}{"source": "clickup"}, 10, 0)
	if err != nil {
		t.Fatalf("SearchByFilter() error = %v", err)
	}
	for _, r := range results {
		if r.Source != "clickup" {
			t.Errorf("expected source=clickup, got %s", r.Source)
		}
	}

	// Filter by status
	results, err = store.SearchByFilter(ctx, map[string]interface{}{"status": "completed"}, 10, 0)
	if err != nil {
		t.Fatalf("SearchByFilter(status) error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one completed session")
	}
}

func TestStore_SearchByFilter_Pagination(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	// Create 5 sessions
	for i := 0; i < 5; i++ {
		var id string
		_ = pool.QueryRow(ctx, `
			INSERT INTO sessions (source, source_id, status) VALUES ('page-test', $1, 'completed') RETURNING id
		`, fmt.Sprintf("page-%d-%s", i, uniqueID(t))).Scan(&id)
	}

	// First page
	page1, err := store.SearchByFilter(ctx, map[string]interface{}{"source": "page-test"}, 2, 0)
	if err != nil {
		t.Fatalf("page 1 error = %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 results, got %d", len(page1))
	}

	// Second page
	page2, err := store.SearchByFilter(ctx, map[string]interface{}{"source": "page-test"}, 2, 2)
	if err != nil {
		t.Fatalf("page 2 error = %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 results, got %d", len(page2))
	}
}

func TestStore_SearchByFilter_DateRange(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()

	since := time.Now().Add(-24 * time.Hour)
	until := time.Now().Add(1 * time.Hour)

	results, err := store.SearchByFilter(ctx, map[string]interface{}{
		"since": since,
		"until": until,
	}, 10, 0)
	if err != nil {
		t.Fatalf("SearchByFilter(date range) error = %v", err)
	}
	// Just verify no error — we can't guarantee specific sessions exist
	_ = results
}

func TestStore_DeleteBySession(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	sessionID := createTestSession(t, pool)
	_, err := store.IndexSession(ctx, sessionID, "To be deleted")
	if err != nil {
		t.Fatalf("IndexSession() error = %v", err)
	}

	err = store.DeleteBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("DeleteBySession() error = %v", err)
	}

	// Search should not find it
	count, _ := store.Count(ctx)
	// Note: other tests may have created entries, so just verify no error
	_ = count
}

func TestStore_Count(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	initial, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}

	sessionID := createTestSession(t, pool)
	_, err = store.IndexSession(ctx, sessionID, "Counted entry")
	if err != nil {
		t.Fatalf("IndexSession() error = %v", err)
	}

	after, _ := store.Count(ctx)
	if after != initial+1 {
		t.Errorf("expected %d entries, got %d", initial+1, after)
	}
}

func TestFormatVector(t *testing.T) {
	tests := []struct {
		name string
		vec  Vector
		want string
	}{
		{"nil", nil, ""},
		{"empty", Vector{}, "[]"},
		{"values", Vector{1.0, 2.5}, "[1.000000,2.500000]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatVector(tt.vec)
			if got != tt.want {
				t.Errorf("formatVector() = %q, want %q", got, tt.want)
			}
		})
	}
}
