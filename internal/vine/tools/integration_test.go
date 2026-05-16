package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
	"github.com/aspectrr/ivy/internal/vine/database"
	"github.com/aspectrr/ivy/internal/vine/eventstore"
	"github.com/aspectrr/ivy/internal/vine/model"
	"github.com/aspectrr/ivy/internal/vine/orchestrator"
	"github.com/aspectrr/ivy/internal/vine/session"
	"github.com/aspectrr/ivy/internal/vine/vine"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This file contains integration tests that test the full lifecycle:
// session creation → sandbox creation → tool execution inside container → teardown.

func dockerHost() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	if _, err := os.Stat("/Users/collinpfeifer/.docker/run/docker.sock"); err == nil {
		return "unix:///Users/collinpfeifer/.docker/run/docker.sock"
	}
	return ""
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := config.DatabaseConfig{
		Host: "localhost", Port: 5432, Name: "ivy",
		User: "ivy", Password: "ivy", SSLMode: "disable",
	}
	pool, err := database.NewPool(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// toolBridge connects the orchestrator's ToolExecutor interface to our Registry
// with a real sandbox attached.
type toolBridge struct {
	reg     *Registry
	sandbox *vine.Sandbox
	events  *eventstore.Store
}

func (b *toolBridge) ExecuteTool(ctx context.Context, name string, args json.RawMessage, sessionID string) (json.RawMessage, error) {
	return b.reg.Execute(ctx, name, args, ToolContext{
		SessionID:  sessionID,
		Sandbox:    b.sandbox,
		EventStore: b.events,
	})
}

func TestToolExecutionInSandbox(t *testing.T) {
	_ = testPool(t)
	ctx := context.Background()

	// Create the sandbox manager.
	mgr, err := vine.NewManager(vine.ManagerConfig{
		DockerHost:  dockerHost(),
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(ctx) }()

	sessionID := uuid.New().String()

	// Create a sandbox.
	sb, err := mgr.Create(ctx, sessionID, vine.SandboxConfig{
		Image:       "debian:bookworm-slim",
		NetworkMode: "none",
	})
	if err != nil {
		t.Fatalf("Create sandbox: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, sessionID) }()

	// Set up the tool registry with sandbox tools.
	reg := NewRegistry()
	if err := RegisterSandboxTools(reg); err != nil {
		t.Fatalf("RegisterSandboxTools: %v", err)
	}

	tctx := ToolContext{
		SessionID: sessionID,
		Sandbox:   sb,
	}

	// --- Test sandbox_bash ---
	t.Run("sandbox_bash", func(t *testing.T) {
		result, err := reg.Execute(ctx, "sandbox_bash", json.RawMessage(`{"command":"echo hello from sandbox"}`), tctx)
		if err != nil {
			t.Fatalf("Execute sandbox_bash: %v", err)
		}

		var output map[string]interface{}
		if err := json.Unmarshal(result, &output); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		stdout := output["stdout"].(string)
		if stdout != "hello from sandbox\n" {
			t.Fatalf("expected 'hello from sandbox\\n', got %q", stdout)
		}
		exitCode := int(output["exit_code"].(float64))
		if exitCode != 0 {
			t.Fatalf("expected exit_code 0, got %d", exitCode)
		}
	})

	// --- Test sandbox_write_file ---
	t.Run("sandbox_write_file", func(t *testing.T) {
		result, err := reg.Execute(ctx, "sandbox_write_file", json.RawMessage(`{"path":"/workspace/test.txt","content":"written by tool"}`), tctx)
		if err != nil {
			t.Fatalf("Execute sandbox_write_file: %v", err)
		}

		var output map[string]interface{}
		if err := json.Unmarshal(result, &output); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if output["status"] != "ok" {
			t.Fatalf("expected status=ok, got %v", output["status"])
		}
	})

	// --- Test sandbox_read_file (reads what we just wrote) ---
	t.Run("sandbox_read_file", func(t *testing.T) {
		result, err := reg.Execute(ctx, "sandbox_read_file", json.RawMessage(`{"path":"/workspace/test.txt"}`), tctx)
		if err != nil {
			t.Fatalf("Execute sandbox_read_file: %v", err)
		}

		var output map[string]interface{}
		if err := json.Unmarshal(result, &output); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		content := output["content"].(string)
		if content != "written by tool" {
			t.Fatalf("expected 'written by tool', got %q", content)
		}
	})

	// --- Test sandbox_bash reads the file too ---
	t.Run("sandbox_bash_reads_written_file", func(t *testing.T) {
		result, err := reg.Execute(ctx, "sandbox_bash", json.RawMessage(`{"command":"cat /workspace/test.txt"}`), tctx)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}

		var output map[string]interface{}
		_ = json.Unmarshal(result, &output)
		stdout := output["stdout"].(string)
		if stdout != "written by tool" {
			t.Fatalf("expected 'written by tool', got %q", stdout)
		}
	})

	// --- Test sandbox_bash multi-line command ---
	t.Run("sandbox_bash_pipeline", func(t *testing.T) {
		cmd := `echo -e "error: kafka down\\ninfo: starting\\nerror: es timeout" | grep "^error:" | wc -l`
		args, _ := json.Marshal(map[string]string{"command": cmd})
		result, err := reg.Execute(ctx, "sandbox_bash", args, tctx)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}

		var output map[string]interface{}
		_ = json.Unmarshal(result, &output)
		stdout := output["stdout"].(string)
		if stdout != "2\n" {
			t.Fatalf("expected '2\\n', got %q", stdout)
		}
	})
}

func TestToolExecutionWithoutSandbox(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSandboxTools(reg)

	ctx := context.Background()
	tctx := ToolContext{SessionID: "test-no-sandbox", Sandbox: nil}

	for _, name := range []string{"sandbox_bash", "sandbox_read_file", "sandbox_write_file"} {
		t.Run(name, func(t *testing.T) {
			_, err := reg.Execute(ctx, name, json.RawMessage(`{"command":"ls","path":"/tmp"}`), tctx)
			if err == nil {
				t.Fatal("expected error when no sandbox is available")
			}
		})
	}
}

func TestFullOrchestratorLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	sessStore := session.NewStore(pool)
	evtStore := eventstore.NewStore(pool)

	// Create the orchestrator with a mock LLM that returns tool calls then a response.
	llm := orchestrator.NewLLMClient(config.LLMConfig{
		Endpoint:     "https://api.example.com/v1",
		APIKey:       "test-key",
		DefaultModel: "test-model",
	})

	orch := orchestrator.New(sessStore, evtStore, llm, nil, slog.Default())

	// Create a session.
	sess, err := sessStore.Create(ctx, "clickup", uuid.New().String(), json.RawMessage(`{"model":"test"}`))
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	// Verify: get session.
	found, err := sessStore.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get session: %v", err)
	}
	if found.Status != model.StatusPending {
		t.Fatalf("expected pending, got %s", found.Status)
	}

	// Add a user message event.
	_, err = evtStore.Append(ctx, sess.ID, model.EventTypeUserMessage, json.RawMessage(`{"content":"debug my kafka pipeline"}`))
	if err != nil {
		t.Fatalf("Append user message: %v", err)
	}

	// Start a run — will fail at LLM call but should transition properly.
	err = orch.StartRun(ctx, sess.ID)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Give the goroutine a moment to process.
	time.Sleep(100 * time.Millisecond)

	// Verify session was set to running (or already terminated due to mock LLM failure).
	updated, err := sessStore.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get after StartRun: %v", err)
	}

	// The agent loop will fail at the LLM call and record an error event.
	// It should have transitioned to running at least.
	events, err := evtStore.GetEvents(ctx, sess.ID, 0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}

	hasTransition := false
	hasUserMsg := false
	for _, evt := range events {
		switch evt.Type {
		case model.EventTypeStatusTransition:
			hasTransition = true
		case model.EventTypeUserMessage:
			hasUserMsg = true
		}
	}
	if !hasTransition {
		t.Fatal("expected a status_transition event")
	}
	if !hasUserMsg {
		t.Fatal("expected a user_message event")
	}

	t.Logf("session %s: status=%s events=%d", sess.ID, updated.Status, len(events))

	// Terminate the session cleanly.
	if err := orch.Terminate(ctx, sess.ID, false); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	final, err := sessStore.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get after Terminate: %v", err)
	}
	if final.Status != model.StatusCompleted {
		t.Fatalf("expected completed, got %s", final.Status)
	}
}

func TestOrchestratorWithRealSandbox(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	sessStore := session.NewStore(pool)
	evtStore := eventstore.NewStore(pool)

	// Create the sandbox manager.
	mgr, err := vine.NewManager(vine.ManagerConfig{
		DockerHost:  dockerHost(),
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(ctx) }()

	// Create session.
	sess, err := sessStore.Create(ctx, "clickup", uuid.New().String(), nil)
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	// Create sandbox for the session.
	sb, err := mgr.Create(ctx, sess.ID, vine.SandboxConfig{
		Image:       "debian:bookworm-slim",
		NetworkMode: "none",
	})
	if err != nil {
		t.Fatalf("Create sandbox: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, sess.ID) }()

	// Link sandbox to session.
	if err := sessStore.SetSandboxID(ctx, sess.ID, sb.ID); err != nil {
		t.Fatalf("SetSandboxID: %v", err)
	}

	// Set up tool registry + bridge.
	reg := NewRegistry()
	_ = RegisterSandboxTools(reg)

	bridge := &toolBridge{reg: reg, sandbox: sb, events: evtStore}

	// Create orchestrator with real tool executor.
	llm := orchestrator.NewLLMClient(config.LLMConfig{
		Endpoint:     "https://api.example.com/v1",
		APIKey:       "test-key",
		DefaultModel: "test-model",
	})
	_ = llm

	orch := orchestrator.New(sessStore, evtStore, llm, bridge, slog.Default())

	// Add a user message.
	_, _ = evtStore.Append(ctx, sess.ID, model.EventTypeUserMessage, json.RawMessage(`{"content":"check my pipeline"}`))

	// Start the run — LLM will fail but the lifecycle should be clean.
	_ = orch.StartRun(ctx, sess.ID)
	time.Sleep(200 * time.Millisecond)

	// Verify events were recorded.
	events, err := evtStore.GetEvents(ctx, sess.ID, 0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}

	t.Logf("session %s has %d events:", sess.ID, len(events))
	for _, evt := range events {
		t.Logf("  seq=%d type=%s", evt.Seq, evt.Type)
	}

	// Clean up.
	_ = orch.Terminate(ctx, sess.ID, false)
}
