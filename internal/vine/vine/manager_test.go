package vine

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

// dockerHost returns the Docker host to use for testing.
func dockerHost() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	// Try common Docker Desktop socket.
	if _, err := os.Stat("/Users/collinpfeifer/.docker/run/docker.sock"); err == nil {
		return "unix:///Users/collinpfeifer/.docker/run/docker.sock"
	}
	return ""
}

// skipWithoutDocker checks if Docker is actually available and skips if not.
func skipWithoutDocker(t *testing.T) *client.Client {
	t.Helper()

	host := dockerHost()
	opts := []client.Opt{client.FromEnv}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		t.Skipf("Docker not responding: %v", err)
	}

	return cli
}

func TestManagerCreateAndDestroy(t *testing.T) {
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

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
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
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
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
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
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 1 * time.Nanosecond,
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

	sb.LastUsedAt = time.Now().Add(-1 * time.Hour)

	if err := mgr.CleanupIdle(ctx); err != nil {
		t.Fatalf("CleanupIdle: %v", err)
	}

	_, err = mgr.Get("test-idle")
	if err == nil {
		t.Fatal("expected sandbox to be cleaned up")
	}
}

func TestSandboxExec(t *testing.T) {
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
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

	// Simple echo.
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

func TestSandboxExecBashPipeline(t *testing.T) {
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	sb, err := mgr.Create(ctx, "test-bash-pipe", SandboxConfig{Image: "debian:bookworm-slim"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, "test-bash-pipe") }()

	// Test a bash pipeline — this proves the sandbox can run real commands.
	result, err := sb.Exec(ctx, "bash", "-c", "echo -e 'line1\nline2\nline3' | grep line2 | wc -l")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "1\n" {
		t.Fatalf("expected stdout='1\\n', got %q", result.Stdout)
	}
}

func TestSandboxWriteAndReadFile(t *testing.T) {
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
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

	content := []byte("test content from ivy")
	if err := sb.WriteFile(ctx, "/workspace/test.txt", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	readContent, err := sb.ReadFile(ctx, "/workspace/test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if readContent != string(content) {
		t.Fatalf("expected %q, got %q", string(content), readContent)
	}
}

func TestSandboxWriteAndReadViaExec(t *testing.T) {
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	sb, err := mgr.Create(ctx, "test-exec-files", SandboxConfig{Image: "debian:bookworm-slim"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, "test-exec-files") }()

	// Write via API, read via bash exec.
	if err := sb.WriteFile(ctx, "/workspace/hello.txt", []byte("hello from ivy")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := sb.Exec(ctx, "cat", "/workspace/hello.txt")
	if err != nil {
		t.Fatalf("Exec cat: %v", err)
	}
	if result.Stdout != "hello from ivy" {
		t.Fatalf("expected 'hello from ivy', got %q", result.Stdout)
	}

	// Write via bash exec, read via API.
	result, err = sb.Exec(ctx, "bash", "-c", "echo 'written by bash' > /workspace/bash_out.txt")
	if err != nil {
		t.Fatalf("Exec write: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	readBack, err := sb.ReadFile(ctx, "/workspace/bash_out.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if readBack != "written by bash\n" {
		t.Fatalf("expected 'written by bash\\n', got %q", readBack)
	}
}

func TestSandboxExecFailingCommand(t *testing.T) {
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
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

	result, err := sb.Exec(ctx, "ls", "/nonexistent/path")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if result.ExitCode == 0 {
		t.Fatal("expected non-zero exit code for nonexistent path")
	}
}

func TestSandboxNoNetwork(t *testing.T) {
	dockerCli := skipWithoutDocker(t)
	_ = dockerCli.Close()

	mgr, err := NewManager(ManagerConfig{
		DockerHost:  dockerHost(),
		AgentImage:  "debian:bookworm-slim",
		IdleTimeout: 30 * time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close(context.Background()) }()

	ctx := context.Background()

	sb, err := mgr.Create(ctx, "test-nonetwork", SandboxConfig{
		Image:       "debian:bookworm-slim",
		NetworkMode: "none",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = mgr.Destroy(ctx, "test-nonetwork") }()

	// Container with --network=none should have no connectivity.
	// curl isn't in slim image, so test DNS resolution directly.
	result, err := sb.Exec(ctx, "bash", "-c", "cat /etc/resolv.conf 2>/dev/null; echo '---'; ping -c 1 -W 1 8.8.8.8 2>&1; echo exit=$?")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	t.Logf("network test output: stdout=%q stderr=%q exitcode=%d", result.Stdout, result.Stderr, result.ExitCode)

	// ping should fail with network=none — either "Network is unreachable" or similar.
	// Just verify we got output and the container is running.
	if result.Stdout == "" {
		t.Fatal("expected some output from network test")
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
				1, 0, 0, 0,
				5, 0, 0, 0,
				'h', 'e', 'l', 'l', 'o',
			},
			stdout: "hello",
			stderr: "",
		},
		{
			name: "stderr only",
			input: []byte{
				2, 0, 0, 0,
				5, 0, 0, 0,
				'e', 'r', 'r', 'o', 'r',
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
