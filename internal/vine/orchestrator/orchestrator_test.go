package orchestrator

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/aspectrr/ivy/internal/vine/eventstore"
	"github.com/aspectrr/ivy/internal/vine/model"
	"github.com/aspectrr/ivy/internal/vine/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

func TestOrchestratorStartRun(t *testing.T) {
	pool := testPool(t)
	sessStore := session.NewStore(pool)
	evtStore := eventstore.NewStore(pool)
	logger := slog.Default()

	// Use a real LLM client config but with dummy endpoint (won't actually call).
	llmCfg := config.LLMConfig{
		Endpoint:     "https://api.example.com/v1",
		APIKey:       "test-key",
		DefaultModel: "test-model",
	}
	llm := NewLLMClient(llmCfg)

	orch := New(sessStore, evtStore, llm, nil, logger)
	ctx := context.Background()

	// Create a session.
	sess, err := sessStore.Create(ctx, "clickup", uuid.New().String(), json.RawMessage(`{"model":"test-model"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Start a run — this will fail at the LLM call but should still transition status.
	err = orch.StartRun(ctx, sess.ID)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Verify status transitioned to running.
	updated, err := sessStore.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get after StartRun: %v", err)
	}
	if updated.Status != model.StatusRunning {
		t.Fatalf("expected status=running, got %s", updated.Status)
	}

	// Verify status_transition event was recorded.
	events, err := evtStore.GetEvents(ctx, sess.ID, 0, 10)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	found := false
	for _, evt := range events {
		if evt.Type == model.EventTypeStatusTransition {
			found = true
			var payload model.StatusTransitionPayload
			if err := json.Unmarshal(evt.Data, &payload); err != nil {
				t.Fatalf("unmarshal transition: %v", err)
			}
			if payload.From != model.StatusPending || payload.To != model.StatusRunning {
				t.Fatalf("unexpected transition: %s -> %s", payload.From, payload.To)
			}
		}
	}
	if !found {
		t.Fatal("expected status_transition event")
	}
}

func TestOrchestratorStartRunInvalidStatus(t *testing.T) {
	pool := testPool(t)
	sessStore := session.NewStore(pool)
	evtStore := eventstore.NewStore(pool)
	logger := slog.Default()
	llm := NewLLMClient(config.LLMConfig{Endpoint: "https://api.example.com/v1", APIKey: "test"})

	orch := New(sessStore, evtStore, llm, nil, logger)
	ctx := context.Background()

	// Create a session and complete it.
	sess, _ := sessStore.Create(ctx, "test", uuid.New().String(), nil)
	_ = sessStore.UpdateStatus(ctx, sess.ID, model.StatusCompleted)

	// Trying to start a completed session should fail.
	err := orch.StartRun(ctx, sess.ID)
	if err == nil {
		t.Fatal("expected error for completed session")
	}
}

func TestOrchestratorTerminate(t *testing.T) {
	pool := testPool(t)
	sessStore := session.NewStore(pool)
	evtStore := eventstore.NewStore(pool)
	logger := slog.Default()
	llm := NewLLMClient(config.LLMConfig{Endpoint: "https://api.example.com/v1", APIKey: "test"})

	orch := New(sessStore, evtStore, llm, nil, logger)
	ctx := context.Background()

	sess, _ := sessStore.Create(ctx, "test", uuid.New().String(), nil)

	// Terminate as success.
	err := orch.Terminate(ctx, sess.ID, false)
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	updated, _ := sessStore.Get(ctx, sess.ID)
	if updated.Status != model.StatusCompleted {
		t.Fatalf("expected status=completed, got %s", updated.Status)
	}
}

func TestOrchestratorTerminateFailed(t *testing.T) {
	pool := testPool(t)
	sessStore := session.NewStore(pool)
	evtStore := eventstore.NewStore(pool)
	logger := slog.Default()
	llm := NewLLMClient(config.LLMConfig{Endpoint: "https://api.example.com/v1", APIKey: "test"})

	orch := New(sessStore, evtStore, llm, nil, logger)
	ctx := context.Background()

	sess, _ := sessStore.Create(ctx, "test", uuid.New().String(), nil)

	err := orch.Terminate(ctx, sess.ID, true)
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	updated, _ := sessStore.Get(ctx, sess.ID)
	if updated.Status != model.StatusFailed {
		t.Fatalf("expected status=failed, got %s", updated.Status)
	}
}

func TestOrchestratorInterrupt(t *testing.T) {
	pool := testPool(t)
	sessStore := session.NewStore(pool)
	evtStore := eventstore.NewStore(pool)
	logger := slog.Default()
	llm := NewLLMClient(config.LLMConfig{Endpoint: "https://api.example.com/v1", APIKey: "test"})

	orch := New(sessStore, evtStore, llm, nil, logger)
	ctx := context.Background()

	sess, _ := sessStore.Create(ctx, "test", uuid.New().String(), nil)
	_ = sessStore.UpdateStatus(ctx, sess.ID, model.StatusRunning)

	err := orch.Interrupt(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	updated, _ := sessStore.Get(ctx, sess.ID)
	if updated.Status != model.StatusSuspended {
		t.Fatalf("expected status=suspended, got %s", updated.Status)
	}

	// Verify interrupt event was recorded.
	events, _ := evtStore.GetEvents(ctx, sess.ID, 0, 10)
	found := false
	for _, evt := range events {
		if evt.Type == model.EventTypeInterrupt {
			found = true
		}
	}
	if !found {
		t.Fatal("expected interrupt event")
	}
}

func TestOrchestratorRetryFromFailed(t *testing.T) {
	pool := testPool(t)
	sessStore := session.NewStore(pool)
	evtStore := eventstore.NewStore(pool)
	logger := slog.Default()
	llm := NewLLMClient(config.LLMConfig{Endpoint: "https://api.example.com/v1", APIKey: "test"})

	orch := New(sessStore, evtStore, llm, nil, logger)
	ctx := context.Background()

	sess, _ := sessStore.Create(ctx, "test", uuid.New().String(), nil)
	_ = sessStore.UpdateStatus(ctx, sess.ID, model.StatusFailed)

	err := orch.Retry(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}

	updated, _ := sessStore.Get(ctx, sess.ID)
	if updated.Status != model.StatusRunning {
		t.Fatalf("expected status=running, got %s", updated.Status)
	}
}
