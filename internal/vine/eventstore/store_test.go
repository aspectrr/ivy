package eventstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/aspectrr/ivy/internal/vine/model"
	"github.com/aspectrr/ivy/internal/vine/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

// createTestSession is a helper that creates a session for event tests.
func createTestSession(t *testing.T, pool *pgxpool.Pool) *model.Session {
	t.Helper()
	sourceID := uuid.New().String()
	sess, err := session.NewStore(pool).Create(context.Background(), "test", sourceID, nil)
	if err != nil {
		t.Fatalf("creating test session: %v", err)
	}
	return sess
}

func TestAppend(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	sess := createTestSession(t, pool)

	evt, err := store.Append(ctx, sess.ID, model.EventTypeUserMessage, json.RawMessage(`{"content":"hello"}`))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	if evt.ID == 0 {
		t.Fatal("expected non-zero event ID")
	}
	if evt.SessionID != sess.ID {
		t.Fatalf("expected session_id=%s, got %s", sess.ID, evt.SessionID)
	}
	if evt.Seq != 1 {
		t.Fatalf("expected seq=1, got %d", evt.Seq)
	}
	if evt.Type != model.EventTypeUserMessage {
		t.Fatalf("expected type=%s, got %s", model.EventTypeUserMessage, evt.Type)
	}
	if evt.CreatedAt.IsZero() {
		t.Fatal("expected non-zero created_at")
	}
}

func TestAppendMonotonicSeq(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	sess := createTestSession(t, pool)

	for i := 1; i <= 5; i++ {
		evt, err := store.Append(ctx, sess.ID, model.EventTypeUserMessage,
			json.RawMessage(`{"content":"msg"}`))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if evt.Seq != int64(i) {
			t.Fatalf("expected seq=%d, got %d", i, evt.Seq)
		}
	}
}

func TestAppendNilData(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	sess := createTestSession(t, pool)

	evt, err := store.Append(ctx, sess.ID, model.EventTypeStatusTransition, nil)
	if err != nil {
		t.Fatalf("Append with nil data: %v", err)
	}
	if string(evt.Data) != "{}" {
		t.Fatalf("expected data={}, got %s", string(evt.Data))
	}
}

func TestGetEvents(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	sess := createTestSession(t, pool)

	// Append 5 events.
	for i := 0; i < 5; i++ {
		_, err := store.Append(ctx, sess.ID, model.EventTypeUserMessage,
			json.RawMessage(`{"content":"msg"}`))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// Get first 3 events (afterSeq=0).
	events, err := store.GetEvents(ctx, sess.ID, 0, 3)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i, evt := range events {
		if evt.Seq != int64(i+1) {
			t.Fatalf("event[%d]: expected seq=%d, got %d", i, i+1, evt.Seq)
		}
	}

	// Get remaining events (afterSeq=3).
	remaining, err := store.GetEvents(ctx, sess.ID, 3, 10)
	if err != nil {
		t.Fatalf("GetEvents remaining: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining events, got %d", len(remaining))
	}
}

func TestGetEventsEmpty(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	sess := createTestSession(t, pool)

	events, err := store.GetEvents(ctx, sess.ID, 0, 10)
	if err != nil {
		t.Fatalf("GetEvents on empty: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestGetLatest(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	sess := createTestSession(t, pool)

	// No events yet.
	latest, err := store.GetLatest(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetLatest empty: %v", err)
	}
	if latest != nil {
		t.Fatalf("expected nil for empty session, got %+v", latest)
	}

	// Append an event.
	_, err = store.Append(ctx, sess.ID, model.EventTypeUserMessage,
		json.RawMessage(`{"content":"first"}`))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	latest, err = store.GetLatest(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if latest.Seq != 1 {
		t.Fatalf("expected seq=1, got %d", latest.Seq)
	}

	// Append another.
	_, err = store.Append(ctx, sess.ID, model.EventTypeAgentMessage,
		json.RawMessage(`{"content":"second"}`))
	if err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	latest, err = store.GetLatest(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetLatest 2: %v", err)
	}
	if latest.Seq != 2 {
		t.Fatalf("expected seq=2, got %d", latest.Seq)
	}
}

func TestGetEventsByType(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	sess := createTestSession(t, pool)

	// Mix of event types.
	_, _ = store.Append(ctx, sess.ID, model.EventTypeUserMessage, json.RawMessage(`{"content":"hi"}`))
	_, _ = store.Append(ctx, sess.ID, model.EventTypeAgentMessage, json.RawMessage(`{"content":"hello"}`))
	_, _ = store.Append(ctx, sess.ID, model.EventTypeToolCall, json.RawMessage(`{"tool_name":"grep"}`))
	_, _ = store.Append(ctx, sess.ID, model.EventTypeUserMessage, json.RawMessage(`{"content":"again"}`))

	// Get only user_messages.
	userEvents, err := store.GetEventsByType(ctx, sess.ID, model.EventTypeUserMessage, 10)
	if err != nil {
		t.Fatalf("GetEventsByType: %v", err)
	}
	if len(userEvents) != 2 {
		t.Fatalf("expected 2 user_message events, got %d", len(userEvents))
	}

	// Should be ordered newest first.
	if userEvents[0].Seq != 4 {
		t.Fatalf("expected first result seq=4 (newest), got %d", userEvents[0].Seq)
	}
	if userEvents[1].Seq != 1 {
		t.Fatalf("expected second result seq=1 (oldest), got %d", userEvents[1].Seq)
	}
}

func TestStreamEvents(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess := createTestSession(t, pool)

	// Append an event before streaming starts.
	_, err := store.Append(ctx, sess.ID, model.EventTypeUserMessage,
		json.RawMessage(`{"content":"before stream"}`))
	if err != nil {
		t.Fatalf("Append before stream: %v", err)
	}

	// Start streaming from seq=0.
	ch, err := store.StreamEvents(ctx, sess.ID, 0)
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	// Should receive the existing event.
	select {
	case evt := <-ch:
		if evt.Seq != 1 {
			t.Fatalf("expected existing event seq=1, got %d", evt.Seq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for existing event")
	}

	// Append a new event — should arrive on the channel.
	_, err = store.Append(ctx, sess.ID, model.EventTypeAgentMessage,
		json.RawMessage(`{"content":"after stream"}`))
	if err != nil {
		t.Fatalf("Append after stream: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Seq != 2 {
			t.Fatalf("expected new event seq=2, got %d", evt.Seq)
		}
		if evt.Type != model.EventTypeAgentMessage {
			t.Fatalf("expected type=agent_message, got %s", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed event")
	}
}

func TestEventPayloads(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	sess := createTestSession(t, pool)

	// Test various payload types round-trip through JSONB.
	payloads := []struct {
		eventType string
		payload   interface{}
	}{
		{
			model.EventTypeUserMessage,
			model.UserMessagePayload{Content: "hello", Attachments: []string{"file.txt"}},
		},
		{
			model.EventTypeAgentMessage,
			model.AgentMessagePayload{Content: "response", Model: "gpt-4o", TokensUsed: 42},
		},
		{
			model.EventTypeToolCall,
			model.ToolCallPayload{ToolName: "grep", Args: json.RawMessage(`{"pattern":"error"}`), CallID: "call_1"},
		},
		{
			model.EventTypeToolResult,
			model.ToolResultPayload{CallID: "call_1", Output: "line 1\nline 2", IsError: false},
		},
		{
			model.EventTypeInterrupt,
			model.InterruptPayload{Reason: "needs confirmation", RequiresAction: true},
		},
		{
			model.EventTypeStatusTransition,
			model.StatusTransitionPayload{From: "running", To: "suspended"},
		},
		{
			model.EventTypeError,
			model.ErrorPayload{Message: "something broke", Recoverable: true},
		},
	}

	for i, tc := range payloads {
		data, err := json.Marshal(tc.payload)
		if err != nil {
			t.Fatalf("marshal payload %d: %v", i, err)
		}

		evt, err := store.Append(ctx, sess.ID, tc.eventType, data)
		if err != nil {
			t.Fatalf("Append payload %d: %v", i, err)
		}

		// Verify we can get it back.
		events, err := store.GetEvents(ctx, sess.ID, evt.Seq-1, 1)
		if err != nil {
			t.Fatalf("GetEvents payload %d: %v", i, err)
		}
		if len(events) != 1 {
			t.Fatalf("payload %d: expected 1 event, got %d", i, len(events))
		}

		var got map[string]interface{}
		if err := json.Unmarshal(events[0].Data, &got); err != nil {
			t.Fatalf("unmarshal payload %d: %v", i, err)
		}
	}
}

func TestEventsIsolatedPerSession(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	sess1 := createTestSession(t, pool)
	sess2 := createTestSession(t, pool)

	// Append events to session 1.
	_, _ = store.Append(ctx, sess1.ID, model.EventTypeUserMessage, json.RawMessage(`{"content":"s1"}`))
	_, _ = store.Append(ctx, sess1.ID, model.EventTypeAgentMessage, json.RawMessage(`{"content":"s1"}`))

	// Append events to session 2.
	_, _ = store.Append(ctx, sess2.ID, model.EventTypeUserMessage, json.RawMessage(`{"content":"s2"}`))

	// Session 1 should have 2 events.
	events1, err := store.GetEvents(ctx, sess1.ID, 0, 10)
	if err != nil {
		t.Fatalf("GetEvents s1: %v", err)
	}
	if len(events1) != 2 {
		t.Fatalf("expected 2 events for s1, got %d", len(events1))
	}

	// Session 2 should have 1 event.
	events2, err := store.GetEvents(ctx, sess2.ID, 0, 10)
	if err != nil {
		t.Fatalf("GetEvents s2: %v", err)
	}
	if len(events2) != 1 {
		t.Fatalf("expected 1 event for s2, got %d", len(events2))
	}

	// Sequence numbers should be independent.
	if events1[0].Seq != 1 || events1[1].Seq != 2 {
		t.Fatalf("s1 events should have seq 1,2; got %d,%d", events1[0].Seq, events1[1].Seq)
	}
	if events2[0].Seq != 1 {
		t.Fatalf("s2 event should have seq 1; got %d", events2[0].Seq)
	}
}
