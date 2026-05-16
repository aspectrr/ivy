package vine

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func skipWithoutDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("IVY_DOCKER_TESTS") == "" {
		t.Skip("skipping Docker integration test (set IVY_DOCKER_TESTS=1 to run)")
	}
}

func TestManagerCreateAndDestroy(t *testing.T) {
	skipWithoutDocker(t)

	mgr, err := NewManager(ManagerConfig{
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	// Create a sandbox.
	sb, err := mgr.Create(ctx, "test-session-1", SandboxConfig{
		Image:       "debian:bookworm-slim",
		NetworkMode: "none",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if sb.ID == "" {
		t.Fatal("expected non-empty container ID")
	}
	if sb.SessionID != "test-session-1" {
		t.Fatalf("expected session_id=test-session-1, got %s", sb.SessionID)
	}
	if sb.CreatedAt.IsZero() {
		t.Fatal("expected non-zero created_at")
	}

	t.Logf("created sandbox: id=%s ip=%s", sb.ID, sb.ContainerIP)

	// Verify it's in the list.
	list := mgr.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 sandbox, got %d", len(list))
	}

	// Get the sandbox.
	found, err := mgr.Get("test-session-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found.ID != sb.ID {
		t.Fatalf("expected id=%s, got %s", sb.ID, found.ID)
	}

	// Destroy it.
	if err := mgr.Destroy(ctx, "test-session-1"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Verify it's gone from the list.
	list = mgr.List()
	if len(list) != 0 {
		t.Fatalf("expected 0 sandboxes after destroy, got %d", len(list))
	}
}

func TestManagerDuplicateSession(t *testing.T) {
	skipWithoutDocker(t)

	mgr, err := NewManager(ManagerConfig{
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	_, err = mgr.Create(ctx, "test-dup", SandboxConfig{Image: "debian:bookworm-slim"})
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}

	_, err = mgr.Create(ctx, "test-dup", SandboxConfig{Image: "debian:bookworm-slim"})
	if err == nil {
		t.Fatal("expected error for duplicate session")
	}

	_ = mgr.Destroy(ctx, "test-dup")
}

func TestManagerGetNotFound(t *testing.T) {
	skipWithoutDocker(t)

	mgr, err := NewManager(ManagerConfig{
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	_, err = mgr.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent sandbox")
	}
}

func TestManagerCleanupIdle(t *testing.T) {
	skipWithoutDocker(t)

	mgr, err := NewManager(ManagerConfig{
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 1 * time.Nanosecond, // Immediately idle.
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	sb, err := mgr.Create(ctx, "test-idle", SandboxConfig{Image: "debian:bookworm-slim"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Set last used to the past.
	sb.LastUsedAt = time.Now().Add(-1 * time.Hour)

	// Run cleanup.
	if err := mgr.CleanupIdle(ctx); err != nil {
		t.Fatalf("CleanupIdle: %v", err)
	}

	// Verify sandbox was cleaned up.
	_, err = mgr.Get("test-idle")
	if err == nil {
		t.Fatal("expected sandbox to be cleaned up")
	}
}

func TestSandboxExec(t *testing.T) {
	skipWithoutDocker(t)

	mgr, err := NewManager(ManagerConfig{
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	sb, err := mgr.Create(ctx, "test-exec", SandboxConfig{Image: "debian:bookworm-slim"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, "test-exec") }()

	// Run a simple command.
	result, err := sb.Exec(ctx, "echo", "hello world")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "hello world\n" {
		t.Fatalf("expected stdout='hello world\\n', got %q", result.Stdout)
	}
}

func TestSandboxWriteAndReadFile(t *testing.T) {
	skipWithoutDocker(t)

	mgr, err := NewManager(ManagerConfig{
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	sb, err := mgr.Create(ctx, "test-files", SandboxConfig{Image: "debian:bookworm-slim"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, "test-files") }()

	// Write a file.
	content := []byte("test content from ivy")
	if err := sb.WriteFile(ctx, "/workspace/test.txt", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Read it back.
	readContent, err := sb.ReadFile(ctx, "/workspace/test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if readContent != string(content) {
		t.Fatalf("expected %q, got %q", string(content), readContent)
	}
}

func TestSandboxExecFailingCommand(t *testing.T) {
	skipWithoutDocker(t)

	mgr, err := NewManager(ManagerConfig{
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	sb, err := mgr.Create(ctx, "test-fail", SandboxConfig{Image: "debian:bookworm-slim"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, "test-fail") }()

	// Run a command that will fail.
	result, err := sb.Exec(ctx, "ls", "/nonexistent/path")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if result.ExitCode == 0 {
		t.Fatal("expected non-zero exit code for nonexistent path")
	}
	if result.Stderr == "" {
		t.Log("note: some Docker versions put errors in stdout for ls")
	}
}

func TestParseMultiplexedOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  []byte
		stdout string
		stderr string
	}{
		{
			name:   "empty",
			input:  []byte{},
			stdout: "",
			stderr: "",
		},
		{
			name: "stdout only",
			input: []byte{
				1, 0, 0, 0, // stream type: stdout
				5, 0, 0, 0, // size: 5
				'h', 'e', 'l', 'l', 'o', // payload
			},
			stdout: "hello",
			stderr: "",
		},
		{
			name: "stderr only",
			input: []byte{
				2, 0, 0, 0, // stream type: stderr
				5, 0, 0, 0, // size: 5
				'e', 'r', 'r', 'o', 'r', // payload
			},
			stdout: "",
			stderr: "error",
		},
		{
			name: "mixed",
			input: []byte{
				1, 0, 0, 0, 3, 0, 0, 0, 'o', 'u', 't',
				2, 0, 0, 0, 3, 0, 0, 0, 'e', 'r', 'r',
			},
			stdout: "out",
			stderr: "err",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := parseMultiplexedOutput(tt.input)
			if stdout != tt.stdout {
				t.Fatalf("stdout: expected %q, got %q", tt.stdout, stdout)
			}
			if stderr != tt.stderr {
				t.Fatalf("stderr: expected %q, got %q", tt.stderr, stderr)
			}
		})
	}
}
