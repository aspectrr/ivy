package leafmgr

import (
	"context"
	"testing"
	"time"

	"log/slog"

	"github.com/aspectrr/ivy/internal/ivyv1"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(slog.Default())
}

func TestManager_RegisterUnregister(t *testing.T) {
	mgr := newTestManager(t)

	conn := &LeafConnection{
		HostID:      "host-001",
		Hostname:    "parser-01",
		AllowedDirs: []string{"/etc/logstash"},
	}

	mgr.RegisterLeaf(conn)

	// Should be listed
	leaves := mgr.ListLeaves()
	if len(leaves) != 1 {
		t.Fatalf("expected 1 leaf, got %d", len(leaves))
	}
	if leaves[0] != "host-001" {
		t.Fatalf("expected host-001, got %s", leaves[0])
	}

	// Should be retrievable
	got, err := mgr.GetLeaf("host-001")
	if err != nil {
		t.Fatalf("GetLeaf: %v", err)
	}
	if got.Hostname != "parser-01" {
		t.Fatalf("expected parser-01, got %s", got.Hostname)
	}

	// Unregister
	mgr.UnregisterLeaf("host-001")
	leaves = mgr.ListLeaves()
	if len(leaves) != 0 {
		t.Fatalf("expected 0 leaves after unregister, got %d", len(leaves))
	}
}

func TestManager_GetLeafNotFound(t *testing.T) {
	mgr := newTestManager(t)

	_, err := mgr.GetLeaf("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent leaf")
	}
}

func TestManager_DefaultLeaf(t *testing.T) {
	mgr := newTestManager(t)

	// No leaves
	_, err := mgr.DefaultLeaf()
	if err == nil {
		t.Fatal("expected error with no leaves")
	}

	// Register a leaf
	mgr.RegisterLeaf(&LeafConnection{HostID: "host-001", Hostname: "parser-01"})

	got, err := mgr.DefaultLeaf()
	if err != nil {
		t.Fatalf("DefaultLeaf: %v", err)
	}
	if got.HostID != "host-001" {
		t.Fatalf("expected host-001, got %s", got.HostID)
	}
}

func TestManager_ResolveHost(t *testing.T) {
	mgr := newTestManager(t)
	mgr.RegisterLeaf(&LeafConnection{HostID: "host-001"})
	mgr.RegisterLeaf(&LeafConnection{HostID: "host-002"})

	// Empty → default (first)
	got, err := mgr.ResolveHost("")
	if err != nil {
		t.Fatalf("ResolveHost empty: %v", err)
	}
	if got.HostID != "host-001" {
		t.Fatalf("expected host-001, got %s", got.HostID)
	}

	// Specific host
	got, err = mgr.ResolveHost("host-002")
	if err != nil {
		t.Fatalf("ResolveHost host-002: %v", err)
	}
	if got.HostID != "host-002" {
		t.Fatalf("expected host-002, got %s", got.HostID)
	}

	// Nonexistent
	_, err = mgr.ResolveHost("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent host")
	}
}

func TestManager_HandleCommandOutput(t *testing.T) {
	mgr := newTestManager(t)

	// Register a pending command
	pending := &PendingCommand{
		Response: make(chan *ivyv1.CommandOutput, 1),
	}
	mgr.mu.Lock()
	mgr.pending["req-123"] = pending
	mgr.mu.Unlock()

	// Simulate output arriving
	output := &ivyv1.CommandOutput{
		RequestId: "req-123",
		ExitCode:  0,
		Stdout:    "hello world",
	}
	mgr.HandleCommandOutput(output)

	select {
	case resp := <-pending.Response:
		if resp.Stdout != "hello world" {
			t.Fatalf("expected 'hello world', got %q", resp.Stdout)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response")
	}
}

func TestManager_HandleCommandOutputUnknown(t *testing.T) {
	mgr := newTestManager(t)

	// Should not panic for unknown request IDs
	output := &ivyv1.CommandOutput{
		RequestId: "unknown-req",
		ExitCode:  0,
		Stdout:    "orphan output",
	}
	mgr.HandleCommandOutput(output)
}

func TestManager_SendCommand_LeafNotConnected(t *testing.T) {
	mgr := newTestManager(t)

	req := &ivyv1.ExecuteCommandRequest{
		RequestId: "req-001",
		Command:   ivyv1.CommandType_GREP,
		Args:      []string{"-n", "error", "/var/log/syslog"},
	}

	_, err := mgr.SendCommand(context.Background(), "nonexistent", req)
	if err == nil {
		t.Fatal("expected error for nonexistent leaf")
	}
}

func TestManager_RegisterReplaces(t *testing.T) {
	mgr := newTestManager(t)

	mgr.RegisterLeaf(&LeafConnection{HostID: "host-001", Hostname: "parser-old"})
	mgr.RegisterLeaf(&LeafConnection{HostID: "host-001", Hostname: "parser-new"})

	got, err := mgr.GetLeaf("host-001")
	if err != nil {
		t.Fatalf("GetLeaf: %v", err)
	}
	if got.Hostname != "parser-new" {
		t.Fatalf("expected parser-new, got %s", got.Hostname)
	}

	// Should still be only 1 leaf
	leaves := mgr.ListLeaves()
	if len(leaves) != 1 {
		t.Fatalf("expected 1 leaf, got %d", len(leaves))
	}
}
