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

func TestManager_SendCommandAndWait_EmptyHost(t *testing.T) {
	mgr := newTestManager(t)

	req := &ivyv1.ExecuteCommandRequest{
		RequestId: "req-001",
		Command:   ivyv1.CommandType_GREP,
		Args:      []string{"-n", "error", "/var/log/syslog"},
	}

	_, err := mgr.SendCommandAndWait(context.Background(), "", req)
	if err == nil {
		t.Fatal("expected error for empty host_id")
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

func TestManager_ListConnectedLeaves(t *testing.T) {
	mgr := newTestManager(t)

	// Empty — no leaves
	infos := mgr.ListConnectedLeaves()
	if len(infos) != 0 {
		t.Fatalf("expected 0 leaves, got %d", len(infos))
	}

	// Register two leaves
	mgr.RegisterLeaf(&LeafConnection{
		HostID:      "host-001",
		Hostname:    "parser-01",
		AllowedDirs: []string{"/etc/logstash"},
	})
	mgr.RegisterLeaf(&LeafConnection{
		HostID:      "host-002",
		Hostname:    "parser-02",
		AllowedDirs: []string{"/etc/logstash", "/var/log"},
	})

	infos = mgr.ListConnectedLeaves()
	if len(infos) != 2 {
		t.Fatalf("expected 2 leaves, got %d", len(infos))
	}

	// Build a map for easier assertions
	byID := make(map[string]LeafInfo)
	for _, info := range infos {
		byID[info.HostID] = info
	}

	if byID["host-001"].Hostname != "parser-01" {
		t.Fatalf("expected parser-01, got %s", byID["host-001"].Hostname)
	}
	if len(byID["host-001"].AllowedDirs) != 1 || byID["host-001"].AllowedDirs[0] != "/etc/logstash" {
		t.Fatalf("unexpected allowed dirs for host-001: %v", byID["host-001"].AllowedDirs)
	}
	if len(byID["host-002"].AllowedDirs) != 2 {
		t.Fatalf("expected 2 allowed dirs for host-002, got %d", len(byID["host-002"].AllowedDirs))
	}

	// Unregister one — should be gone
	mgr.UnregisterLeaf("host-001")
	infos = mgr.ListConnectedLeaves()
	if len(infos) != 1 {
		t.Fatalf("expected 1 leaf after unregister, got %d", len(infos))
	}
	if infos[0].HostID != "host-002" {
		t.Fatalf("expected host-002, got %s", infos[0].HostID)
	}
}
