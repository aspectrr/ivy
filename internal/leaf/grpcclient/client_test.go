package grpcclient

import (
	"testing"

	"github.com/aspectrr/ivy/internal/ivyv1"
)

func TestProtoCmdToString(t *testing.T) {
	tests := []struct {
		cmd      ivyv1.CommandType
		expected string
		ok       bool
	}{
		{ivyv1.CommandType_GREP, "grep", true},
		{ivyv1.CommandType_AWK, "awk", true},
		{ivyv1.CommandType_FIND, "find", true},
		{ivyv1.CommandType_CAT, "cat", true},
		{ivyv1.CommandType_READ_FILE, "cat", true},
		{ivyv1.CommandType_TAIL, "tail", true},
		{ivyv1.CommandType_SYSTEMCTL_STATUS, "systemctl", true},
		{ivyv1.CommandType_JOURNALCTL, "journalctl", true},
		{ivyv1.CommandType_COMMAND_TYPE_UNSPECIFIED, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.cmd.String(), func(t *testing.T) {
			name, ok := protoCmdToString(tt.cmd)
			if ok != tt.ok {
				t.Fatalf("ok: got %v, want %v", ok, tt.ok)
			}
			if name != tt.expected {
				t.Fatalf("name: got %q, want %q", name, tt.expected)
			}
		})
	}
}

func TestClientConfig_Defaults(t *testing.T) {
	cfg := ClientConfig{
		VineAddress: "localhost:50051",
		HostID:      "test-host-001",
		Hostname:    "parser-01",
		AllowedDirs: []string{"/etc/logstash"},
	}

	if cfg.VineAddress != "localhost:50051" {
		t.Fatalf("unexpected address: %s", cfg.VineAddress)
	}
	if cfg.HostID != "test-host-001" {
		t.Fatalf("unexpected host_id: %s", cfg.HostID)
	}
	if len(cfg.AllowedDirs) != 1 {
		t.Fatalf("expected 1 allowed dir, got %d", len(cfg.AllowedDirs))
	}
}

func TestClient_IsConnected_Initially(t *testing.T) {
	cfg := ClientConfig{VineAddress: "localhost:50051"}
	client := NewClient(cfg, nil, nil)

	if client.IsConnected() {
		t.Fatal("new client should not be connected")
	}
}

func TestClient_Close_WithoutConnect(t *testing.T) {
	cfg := ClientConfig{VineAddress: "localhost:50051"}
	client := NewClient(cfg, nil, nil)

	if err := client.Close(); err != nil {
		t.Fatalf("Close without connect should not error: %v", err)
	}
}

func TestCommandResult_Structure(t *testing.T) {
	r := &CommandResult{
		RequestID: "req-123",
		ExitCode:  0,
		Stdout:    "hello world",
		Stderr:    "",
		Timeout:   false,
	}

	if r.RequestID != "req-123" {
		t.Fatalf("unexpected request ID: %s", r.RequestID)
	}
	if r.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", r.ExitCode)
	}
	if r.Stdout != "hello world" {
		t.Fatalf("unexpected stdout: %s", r.Stdout)
	}
}
