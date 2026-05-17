package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "vine.yaml")

	content := []byte(`
server:
  grpc_port: 50051
  http_port: 8080
  grpc_tls:
    cert: /path/to/cert.pem
    key: /path/to/key.pem
    ca: /path/to/ca.pem

database:
  host: db.example.com
  port: 5432
  name: ivydb
  user: ivyuser
  password: fromfile
  sslmode: require

llm:
  endpoint: "https://api.openai.com/v1"
  api_key: "file-key"
  default_model: "gpt-4o"
  embedding_model: "text-embedding-ada-002"

sandbox:
  docker_host: "unix:///var/run/docker.sock"
  agent_image: "ivy-agent:latest"
  pipeline_image: "ivy-pipeline:latest"
  max_concurrent: 5
  idle_timeout: "15m"
  cpu_limit: "2.0"
  memory_limit: "2g"

connectors:
  clickup:
    enabled: true
    webhook_secret: "file-wh-secret"
    api_token: "file-api-token"
    team_id: "team789"
    list_id: "list123"
    space_id: "space456"
    tag: "ivy"
    assignee: "user1"
    poll_interval: "30s"
    proxy: "http://proxy.corp.internal:3128"
`)

	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Server
	if cfg.Server.GRPCPort != 50051 {
		t.Errorf("Server.GRPCPort = %d, want 50051", cfg.Server.GRPCPort)
	}
	if cfg.Server.HTTPPort != 8080 {
		t.Errorf("Server.HTTPPort = %d, want 8080", cfg.Server.HTTPPort)
	}
	if cfg.Server.GRPCTLS.Cert != "/path/to/cert.pem" {
		t.Errorf("Server.GRPCTLS.Cert = %q, want /path/to/cert.pem", cfg.Server.GRPCTLS.Cert)
	}

	// Database
	if cfg.Database.Host != "db.example.com" {
		t.Errorf("Database.Host = %q, want db.example.com", cfg.Database.Host)
	}
	if cfg.Database.Port != 5432 {
		t.Errorf("Database.Port = %d, want 5432", cfg.Database.Port)
	}
	if cfg.Database.Name != "ivydb" {
		t.Errorf("Database.Name = %q, want ivydb", cfg.Database.Name)
	}

	// DSN
	expectedDSN := "postgres://ivyuser:fromfile@db.example.com:5432/ivydb?sslmode=require"
	if got := cfg.Database.DSN(); got != expectedDSN {
		t.Errorf("DSN() = %q, want %q", got, expectedDSN)
	}

	// LLM
	if cfg.LLM.Endpoint != "https://api.openai.com/v1" {
		t.Errorf("LLM.Endpoint = %q, want https://api.openai.com/v1", cfg.LLM.Endpoint)
	}
	if cfg.LLM.DefaultModel != "gpt-4o" {
		t.Errorf("LLM.DefaultModel = %q, want gpt-4o", cfg.LLM.DefaultModel)
	}

	// Sandbox
	if cfg.Sandbox.MaxConcurrent != 5 {
		t.Errorf("Sandbox.MaxConcurrent = %d, want 5", cfg.Sandbox.MaxConcurrent)
	}
	if cfg.Sandbox.AgentImage != "ivy-agent:latest" {
		t.Errorf("Sandbox.AgentImage = %q, want ivy-agent:latest", cfg.Sandbox.AgentImage)
	}

	// Connectors
	if !cfg.Connectors.ClickUp.Enabled {
		t.Error("Connectors.ClickUp.Enabled = false, want true")
	}
	if cfg.Connectors.ClickUp.ListID != "list123" {
		t.Errorf("Connectors.ClickUp.ListID = %q, want list123", cfg.Connectors.ClickUp.ListID)
	}
	if cfg.Connectors.ClickUp.TeamID != "team789" {
		t.Errorf("Connectors.ClickUp.TeamID = %q, want team789", cfg.Connectors.ClickUp.TeamID)
	}
	if cfg.Connectors.ClickUp.Tag != "ivy" {
		t.Errorf("Connectors.ClickUp.Tag = %q, want ivy", cfg.Connectors.ClickUp.Tag)
	}
	if cfg.Connectors.ClickUp.Proxy != "http://proxy.corp.internal:3128" {
		t.Errorf("Connectors.ClickUp.Proxy = %q, want http://proxy.corp.internal:3128", cfg.Connectors.ClickUp.Proxy)
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "vine.yaml")

	content := []byte(`
server:
  grpc_port: 50051
  http_port: 8080
database:
  host: localhost
  port: 5432
  name: ivy
  user: ivy
  password: file-password
  sslmode: disable
llm:
  endpoint: "https://api.openai.com/v1"
  api_key: "file-key"
  default_model: "gpt-4o"
  embedding_model: "text-embedding-ada-002"
sandbox:
  docker_host: "unix:///var/run/docker.sock"
  agent_image: "ivy-agent:latest"
  pipeline_image: "ivy-pipeline:latest"
  max_concurrent: 10
  idle_timeout: "30m"
  cpu_limit: "1.0"
  memory_limit: "1g"
connectors:
  clickup:
    enabled: false
`)

	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	// Set env overrides
	t.Setenv("IVY_DB_PASSWORD", "env-db-password")
	t.Setenv("IVY_LLM_API_KEY", "env-llm-key")
	t.Setenv("IVY_CLICKUP_WEBHOOK_SECRET", "env-wh-secret")
	t.Setenv("IVY_CLICKUP_API_TOKEN", "env-cu-token")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.Database.Password != "env-db-password" {
		t.Errorf("Database.Password = %q, want env-db-password", cfg.Database.Password)
	}
	if cfg.LLM.APIKey != "env-llm-key" {
		t.Errorf("LLM.APIKey = %q, want env-llm-key", cfg.LLM.APIKey)
	}
	if cfg.Connectors.ClickUp.WebhookSecret != "env-wh-secret" {
		t.Errorf("ClickUp.WebhookSecret = %q, want env-wh-secret", cfg.Connectors.ClickUp.WebhookSecret)
	}
	if cfg.Connectors.ClickUp.APIToken != "env-cu-token" {
		t.Errorf("ClickUp.APIToken = %q, want env-cu-token", cfg.Connectors.ClickUp.APIToken)
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

func TestDatabaseConfig_DSN(t *testing.T) {
	tests := []struct {
		name     string
		config   DatabaseConfig
		expected string
	}{
		{
			name: "standard DSN",
			config: DatabaseConfig{
				Host: "localhost", Port: 5432, Name: "ivy",
				User: "user", Password: "pass", SSLMode: "disable",
			},
			expected: "postgres://user:pass@localhost:5432/ivy?sslmode=disable",
		},
		{
			name: "with SSL",
			config: DatabaseConfig{
				Host: "db.prod", Port: 5433, Name: "ivyprod",
				User: "admin", Password: "s3cret", SSLMode: "require",
			},
			expected: "postgres://admin:s3cret@db.prod:5433/ivyprod?sslmode=require",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.DSN()
			if got != tt.expected {
				t.Errorf("DSN() = %q, want %q", got, tt.expected)
			}
		})
	}
}
