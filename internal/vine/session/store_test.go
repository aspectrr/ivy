package session

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueID returns a unique source_id for each test invocation.
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

func TestCreate(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	sess, err := store.Create(ctx, "clickup", uniqueID(t), json.RawMessage(`{"model":"gpt-4o"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if sess.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if sess.Source != "clickup" {
		t.Fatalf("expected source=clickup, got %s", sess.Source)
	}
	if sess.Status != "pending" {
		t.Fatalf("expected status=pending, got %s", sess.Status)
	}
	if sess.SandboxID != nil {
		t.Fatalf("expected nil sandbox_id, got %v", sess.SandboxID)
	}
	if sess.CreatedAt.IsZero() {
		t.Fatal("expected non-zero created_at")
	}

	t.Logf("created session: id=%s status=%s", sess.ID, sess.Status)
}

func TestGet(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	created, err := store.Create(ctx, "clickup", uniqueID(t), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	found, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found.ID != created.ID {
		t.Fatalf("expected id=%s, got %s", created.ID, found.ID)
	}
}

func TestGetNotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	_, err := store.Get(ctx, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestGetBySource(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	sid := uniqueID(t)
	created, err := store.Create(ctx, "clickup", sid, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	found, err := store.GetBySource(ctx, "clickup", sid)
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if found.ID != created.ID {
		t.Fatalf("expected id=%s, got %s", created.ID, found.ID)
	}
}

func TestGetBySourceNotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	_, err := store.GetBySource(ctx, "nonexistent", "nope")
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestUpdateStatus(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	sess, err := store.Create(ctx, "clickup", uniqueID(t), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.UpdateStatus(ctx, sess.ID, "running"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	updated, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if updated.Status != "running" {
		t.Fatalf("expected status=running, got %s", updated.Status)
	}
	if !updated.UpdatedAt.After(sess.UpdatedAt) {
		t.Fatal("expected updated_at to change after status update")
	}
}

func TestSetSandboxID(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	sess, err := store.Create(ctx, "clickup", uniqueID(t), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	sandboxID := "container_abc123"
	if err := store.SetSandboxID(ctx, sess.ID, sandboxID); err != nil {
		t.Fatalf("SetSandboxID: %v", err)
	}

	updated, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get after SetSandboxID: %v", err)
	}
	if updated.SandboxID == nil || *updated.SandboxID != sandboxID {
		t.Fatalf("expected sandbox_id=%s, got %v", sandboxID, updated.SandboxID)
	}
}

func TestClearSandboxID(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	sess, err := store.Create(ctx, "clickup", uniqueID(t), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.SetSandboxID(ctx, sess.ID, "container_xyz"); err != nil {
		t.Fatalf("SetSandboxID: %v", err)
	}
	if err := store.ClearSandboxID(ctx, sess.ID); err != nil {
		t.Fatalf("ClearSandboxID: %v", err)
	}

	updated, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get after ClearSandboxID: %v", err)
	}
	if updated.SandboxID != nil {
		t.Fatalf("expected nil sandbox_id, got %v", updated.SandboxID)
	}
}

func TestListByStatus(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	sess1, err := store.Create(ctx, "clickup", uniqueID(t), nil)
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if err := store.UpdateStatus(ctx, sess1.ID, "running"); err != nil {
		t.Fatalf("UpdateStatus 1: %v", err)
	}

	sess2, err := store.Create(ctx, "clickup", uniqueID(t), nil)
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	if err := store.UpdateStatus(ctx, sess2.ID, "running"); err != nil {
		t.Fatalf("UpdateStatus 2: %v", err)
	}

	results, err := store.ListByStatus(ctx, "running", 10, 0)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}

	ids := make(map[string]bool)
	for _, s := range results {
		ids[s.ID] = true
	}
	if !ids[sess1.ID] || !ids[sess2.ID] {
		t.Fatalf("expected to find both sessions in listing, got %d results", len(results))
	}
}

func TestListByStatusPagination(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	var created []string
	for i := 0; i < 3; i++ {
		sess, err := store.Create(ctx, "test_pagination", uniqueID(t), nil)
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		created = append(created, sess.ID)
	}

	// Get first page.
	page1, err := store.ListByStatus(ctx, "pending", 2, 0)
	if err != nil {
		t.Fatalf("ListByStatus page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected page 1 to have 2 results, got %d", len(page1))
	}

	// Get second page.
	page2, err := store.ListByStatus(ctx, "pending", 2, 2)
	if err != nil {
		t.Fatalf("ListByStatus page 2: %v", err)
	}
	if len(page2) < 1 {
		t.Fatalf("expected page 2 to have at least 1 result, got %d", len(page2))
	}

	// Ensure no overlap between pages.
	seen := make(map[string]bool)
	for _, s := range page1 {
		if seen[s.ID] {
			t.Fatalf("duplicate session %s across pages", s.ID)
		}
		seen[s.ID] = true
	}
	for _, s := range page2 {
		if seen[s.ID] {
			t.Fatalf("duplicate session %s across pages", s.ID)
		}
		seen[s.ID] = true
	}

	// Ensure all our created sessions are somewhere in the results.
	for _, id := range created {
		if !seen[id] {
			t.Fatalf("created session %s not found in either page", id)
		}
	}
}

func TestUpdateMetadata(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	sess, err := store.Create(ctx, "clickup", uniqueID(t), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if string(sess.Metadata) != "{}" {
		t.Fatalf("expected initial metadata={}, got %s", string(sess.Metadata))
	}

	if err := store.UpdateMetadata(ctx, sess.ID, json.RawMessage(`{"assignee":"alice"}`)); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	updated, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get after UpdateMetadata: %v", err)
	}

	var meta map[string]string
	if err := json.Unmarshal(updated.Metadata, &meta); err != nil {
		t.Fatalf("unmarshaling metadata: %v", err)
	}
	if meta["assignee"] != "alice" {
		t.Fatalf("expected assignee=alice, got %v", meta)
	}
}

func TestUpdateStatusNotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	err := store.UpdateStatus(ctx, "00000000-0000-0000-0000-000000000000", "running")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}
