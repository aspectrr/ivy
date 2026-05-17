package skills

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
	// Deterministic fake embedding based on text content
	vec := make(Vector, m.dim)
	for i := range vec {
		vec[i] = float32(len(text)*100 + i)
	}
	return vec, nil
}

// testStore creates a skills store for integration testing.
func testStore(t *testing.T) (*Store, func()) {
	t.Helper()
	pool := testPool(t)
	store := NewStore(pool, &mockEmbedder{dim: 768})
	return store, func() {}
}

func TestFormatVector(t *testing.T) {
	tests := []struct {
		name string
		vec  Vector
		want string
	}{
		{"nil", nil, ""},
		{"empty", Vector{}, "[]"},
		{"single", Vector{1.0}, "[1.000000]"},
		{"multiple", Vector{1.0, 2.5, 3.7}, "[1.000000,2.500000,3.700000]"},
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

func TestParseVector(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Vector
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"null", "null", nil, false},
		{"empty brackets", "[]", Vector{}, false},
		{"single", "[1.500000]", Vector{1.5}, false},
		{"multiple", "[1.000000,2.500000,3.700000]", Vector{1.0, 2.5, 3.7}, false},
		{"invalid", "not a vector", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVector(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("length = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				diff := got[i] - tt.want[i]
				if diff > 0.001 || diff < -0.001 {
					t.Errorf("got[%d] = %f, want %f", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFormatParseRoundtrip(t *testing.T) {
	original := Vector{1.5, 2.7, 3.14159, 0.0, -1.2}
	formatted := formatVector(original)
	parsed, err := parseVector(formatted)
	if err != nil {
		t.Fatalf("parseVector error: %v", err)
	}
	if len(parsed) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(parsed), len(original))
	}
	for i := range original {
		diff := parsed[i] - original[i]
		if diff > 0.001 || diff < -0.001 {
			t.Errorf("element %d: got %f, want %f", i, parsed[i], original[i])
		}
	}
}

func TestStore_CreateAndGet(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()
	skill, err := store.Create(ctx, "test-skill-"+uniqueID(t), "A test skill", "Do the thing", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if skill.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if skill.BuiltIn {
		t.Error("should not be built-in")
	}

	got, err := store.Get(ctx, skill.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != skill.Name {
		t.Errorf("name = %q, want %q", got.Name, skill.Name)
	}
	if got.Content != skill.Content {
		t.Errorf("content mismatch")
	}
}

func TestStore_GetByName(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()
	name := "named-skill-" + uniqueID(t)
	_, err := store.Create(ctx, name, "Named", "Content", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := store.GetByName(ctx, name)
	if err != nil {
		t.Fatalf("GetByName() error = %v", err)
	}
	if got.Name != name {
		t.Errorf("name = %q", got.Name)
	}

	_, err = store.GetByName(ctx, "nonexistent-"+uniqueID(t))
	if err == nil {
		t.Error("expected error for nonexistent skill")
	}
}

func TestStore_Search(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()

	// Create skills with distinct names for search
	skills := []struct {
		name, desc, content string
	}{
		{"kafka-" + uniqueID(t), "Debugging Kafka consumer lag issues", "Check consumer groups"},
		{"es-" + uniqueID(t), "Elasticsearch query patterns for log search", "Use bool queries"},
		{"logstash-" + uniqueID(t), "Logstash pipeline configuration", "Use multiline codec"},
	}
	for _, s := range skills {
		_, err := store.Create(ctx, s.name, s.desc, s.content, nil)
		if err != nil {
			t.Fatalf("Create(%s) error = %v", s.name, err)
		}
	}

	// Generate a query embedding and search
	queryVec, _ := store.embeds.Embed(ctx, "kafka consumer problems")
	results, err := store.Search(ctx, queryVec, 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	t.Logf("Search returned %d results, first: %s", len(results), results[0].Name)
}

func TestStore_Update(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()
	skill, err := store.Create(ctx, "updatable-"+uniqueID(t), "Test", "Original content", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = store.Update(ctx, skill.ID, "Updated content")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ := store.Get(ctx, skill.ID)
	if got.Content != "Updated content" {
		t.Errorf("content = %q, want 'Updated content'", got.Content)
	}
}

func TestStore_Delete(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()
	skill, err := store.Create(ctx, "deletable-"+uniqueID(t), "Test", "Content", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	err = store.Delete(ctx, skill.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err = store.Get(ctx, skill.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestStore_UpsertBuiltIn(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()

	vec := make(Vector, 768)
	for i := range vec {
		vec[i] = float32(i)
	}

	name := "builtin-" + uniqueID(t)

	// First insert
	skill, err := store.UpsertBuiltIn(ctx, name, "Built-in", "Content", vec)
	if err != nil {
		t.Fatalf("UpsertBuiltIn() error = %v", err)
	}
	if !skill.BuiltIn {
		t.Error("expected built_in = true")
	}

	// Upsert should update
	newVec := make(Vector, 768)
	for i := range newVec {
		newVec[i] = float32(i + 100)
	}
	updated, err := store.UpsertBuiltIn(ctx, name, "Updated desc", "New content", newVec)
	if err != nil {
		t.Fatalf("UpsertBuiltIn(2) error = %v", err)
	}
	if updated.Description != "Updated desc" {
		t.Errorf("description = %q, want 'Updated desc'", updated.Description)
	}

	// Should still be only one skill with that name
	count := 0
	_ = store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM skills WHERE name = $1`, name).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 skill, got %d", count)
	}
}

func TestStore_ListAll(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()
	_, _ = store.Create(ctx, "list-a-"+uniqueID(t), "A", "Content A", nil)
	_, _ = store.Create(ctx, "list-b-"+uniqueID(t), "B", "Content B", nil)

	list, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(list) < 2 {
		t.Errorf("expected at least 2 skills, got %d", len(list))
	}
}

func TestStore_RecordUsage(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()
	skill, _ := store.Create(ctx, "usage-"+uniqueID(t), "Test", "Content", nil)

	// Create a session to reference
	var sessionID string
	_ = store.pool.QueryRow(ctx, `
		INSERT INTO sessions (source, source_id, status) VALUES ('test', $1, 'pending') RETURNING id
	`, fmt.Sprintf("usage-test-%d", time.Now().Unix())).Scan(&sessionID)

	usageID, err := store.RecordUsage(ctx, skill.ID, sessionID)
	if err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	if usageID == 0 {
		t.Error("expected non-zero usage ID")
	}

	// Mark helpful
	err = store.MarkHelpful(ctx, usageID, true)
	if err != nil {
		t.Fatalf("MarkHelpful() error = %v", err)
	}

	usage, err := store.GetUsage(ctx, usageID)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.WasHelpful == nil || !*usage.WasHelpful {
		t.Error("expected was_helpful = true")
	}
}

func TestStore_Count(t *testing.T) {
	store, cleanup := testStore(t)
	defer cleanup()

	ctx := context.Background()

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}

	_, _ = store.Create(ctx, "count-"+uniqueID(t), "Test", "Content", nil)

	newCount, _ := store.Count(ctx)
	if newCount != count+1 {
		t.Errorf("expected %d skills, got %d", count+1, newCount)
	}
}
