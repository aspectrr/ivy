package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "leaf.yaml")

	content := []byte(`
vine:
  address: "vine.internal:50051"
  tls:
    cert: /etc/ivy-leaf/certs/client.pem
    key: /etc/ivy-leaf/certs/client-key.pem
    ca: /etc/ivy-leaf/certs/ca.pem
  reconnect_interval: "10s"

allowed_directories:
  - "/etc/logstash"
  - "/var/log/logstash"
  - "/opt/logstash/pipelines"

allowed_commands:
  - grep
  - awk
  - find
  - cat
  - tail
  - systemctl
  - journalctl

max_concurrent_commands: 5
command_timeout: "60s"
log_level: "debug"
`)

	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Vine connection
	if cfg.Vine.Address != "vine.internal:50051" {
		t.Errorf("Vine.Address = %q, want vine.internal:50051", cfg.Vine.Address)
	}
	if cfg.Vine.TLS.Cert != "/etc/ivy-leaf/certs/client.pem" {
		t.Errorf("Vine.TLS.Cert = %q, unexpected", cfg.Vine.TLS.Cert)
	}
	if cfg.Vine.ReconnectInterval != 10*time.Second {
		t.Errorf("Vine.ReconnectInterval = %v, want 10s", cfg.Vine.ReconnectInterval)
	}

	// Allowed directories
	expectedDirs := []string{"/etc/logstash", "/var/log/logstash", "/opt/logstash/pipelines"}
	if len(cfg.AllowedDirectories) != len(expectedDirs) {
		t.Fatalf("AllowedDirectories len = %d, want %d", len(cfg.AllowedDirectories), len(expectedDirs))
	}
	for i, dir := range expectedDirs {
		if cfg.AllowedDirectories[i] != dir {
			t.Errorf("AllowedDirectories[%d] = %q, want %q", i, cfg.AllowedDirectories[i], dir)
		}
	}

	// Allowed commands
	expectedCmds := []string{"grep", "awk", "find", "cat", "tail", "systemctl", "journalctl"}
	if len(cfg.AllowedCommands) != len(expectedCmds) {
		t.Fatalf("AllowedCommands len = %d, want %d", len(cfg.AllowedCommands), len(expectedCmds))
	}
	for i, cmd := range expectedCmds {
		if cfg.AllowedCommands[i] != cmd {
			t.Errorf("AllowedCommands[%d] = %q, want %q", i, cfg.AllowedCommands[i], cmd)
		}
	}

	// Other fields
	if cfg.MaxConcurrentCmds != 5 {
		t.Errorf("MaxConcurrentCmds = %d, want 5", cfg.MaxConcurrentCmds)
	}
	if cfg.CommandTimeout != 60*time.Second {
		t.Errorf("CommandTimeout = %v, want 60s", cfg.CommandTimeout)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "leaf.yaml")

	// Minimal config — only required fields
	content := []byte(`
vine:
  address: "localhost:50051"
allowed_directories:
  - "/etc/logstash"
allowed_commands:
  - grep
`)

	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Vine.Address != "localhost:50051" {
		t.Errorf("Vine.Address = %q, want localhost:50051", cfg.Vine.Address)
	}
	if len(cfg.AllowedDirectories) != 1 {
		t.Errorf("AllowedDirectories len = %d, want 1", len(cfg.AllowedDirectories))
	}
	if len(cfg.AllowedCommands) != 1 {
		t.Errorf("AllowedCommands len = %d, want 1", len(cfg.AllowedCommands))
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("LoadConfig() should return error for missing file")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "bad.yaml")

	if err := os.WriteFile(cfgPath, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("LoadConfig() should return error for invalid YAML")
	}
}
