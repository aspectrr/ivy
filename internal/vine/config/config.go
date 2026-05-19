package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the configuration for vine.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Database   DatabaseConfig   `yaml:"database"`
	LLM        LLMConfig        `yaml:"llm"`
	Sandbox    SandboxConfig    `yaml:"sandbox"`
	Connectors ConnectorsConfig `yaml:"connectors"`
}

type ServerConfig struct {
	GRPCPort int       `yaml:"grpc_port"`
	HTTPPort int       `yaml:"http_port"`
	GRPCTLS  TLSConfig `yaml:"grpc_tls"`
}

type TLSConfig struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
	CA   string `yaml:"ca"`
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	SSLMode  string `yaml:"sslmode"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.Name, d.SSLMode,
	)
}

type LLMConfig struct {
	Endpoint       string `yaml:"endpoint"`
	APIKey         string `yaml:"api_key"`
	DefaultModel   string `yaml:"default_model"`
	EmbeddingModel string `yaml:"embedding_model"`
	EmbeddingDim   int    `yaml:"embedding_dim"`
}

type SandboxConfig struct {
	DockerHost    string        `yaml:"docker_host"`
	AgentImage    string        `yaml:"agent_image"`
	PipelineImage string        `yaml:"pipeline_image"`
	MaxConcurrent int           `yaml:"max_concurrent"`
	IdleTimeout   time.Duration `yaml:"idle_timeout"`
	CPULimit      string        `yaml:"cpu_limit"`
	MemoryLimit   string        `yaml:"memory_limit"`
}

type ConnectorsConfig struct {
	ClickUp ClickUpConfig `yaml:"clickup"`
	GitHub  GitHubConfig  `yaml:"github"`
}

// GitHubConfig holds configuration for the GitHub App integration.
// The agent uses a GitHub App (server-to-server) to push branches,
// open PRs, and comment — all attributed to app-name[bot].
type GitHubConfig struct {
	Enabled        bool   `yaml:"enabled"`
	AppID          int64  `yaml:"app_id"`
	PrivateKeyPath string `yaml:"private_key_path"` // Path to PEM file (alternative to PrivateKey)
	PrivateKey     string `yaml:"private_key"`      // Raw PEM content (overrides PrivateKeyPath)
	BaseURL        string `yaml:"base_url"`         // API base URL; defaults to https://api.github.com. For GHES: https://github.example.com/api/v3
	BranchPrefix   string `yaml:"branch_prefix"`    // Required prefix for agent branches (default: "agent/")
	ProtectedRefs  string `yaml:"protected_refs"`   // Comma-separated globs of protected refs (default: "main,master,release/*")
}

type ClickUpConfig struct {
	Enabled       bool          `yaml:"enabled"`
	AuthMode      string        `yaml:"auth_mode"` // "personal" (pk_ token) or "oauth" (bearer token). Default: auto-detected from token prefix.
	APIToken      string        `yaml:"api_token"` // Personal token (pk_...) or OAuth access token
	TeamID        string        `yaml:"team_id"`
	ListID        string        `yaml:"list_id"`
	SpaceID       string        `yaml:"space_id"`
	Tag           string        `yaml:"tag"`
	Assignee      string        `yaml:"assignee"`
	PollInterval  time.Duration `yaml:"poll_interval"`
	Proxy         string        `yaml:"proxy"`
	WebhookSecret string        `yaml:"webhook_secret"`
	AgentUsername string        `yaml:"agent_username"` // ClickUp username to detect @mentions in comments (default: IvyAgent)
}

// DefaultAgentUsername is used when agent_username is not set.
const DefaultAgentUsername = "Ivy Agent"

// AuthModeResolved returns the effective auth mode, auto-detecting from the
// token prefix if not explicitly set.
func (c ClickUpConfig) AuthModeResolved() string {
	if c.AuthMode != "" {
		return c.AuthMode
	}
	if strings.HasPrefix(c.APIToken, "pk_") {
		return "personal"
	}
	return "oauth"
}

// LoadHostdConfig loads the host daemon configuration from a YAML file.
// Environment variables can override sensitive values.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Override with environment variables
	if v := os.Getenv("IVY_DB_HOST"); v != "" {
		cfg.Database.Host = v
	}
	if v := os.Getenv("IVY_DB_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
	if v := os.Getenv("IVY_LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("IVY_LLM_ENDPOINT"); v != "" {
		cfg.LLM.Endpoint = v
	}
	if v := os.Getenv("IVY_LLM_MODEL"); v != "" {
		cfg.LLM.DefaultModel = v
	}
	if v := os.Getenv("IVY_CLICKUP_ENABLED"); v != "" {
		cfg.Connectors.ClickUp.Enabled = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("IVY_CLICKUP_WEBHOOK_SECRET"); v != "" {
		cfg.Connectors.ClickUp.WebhookSecret = v
	}
	if v := os.Getenv("IVY_CLICKUP_API_TOKEN"); v != "" {
		cfg.Connectors.ClickUp.APIToken = v
	}
	if v := os.Getenv("IVY_CLICKUP_TEAM_ID"); v != "" {
		cfg.Connectors.ClickUp.TeamID = v
	}
	if v := os.Getenv("IVY_CLICKUP_SPACE_ID"); v != "" {
		cfg.Connectors.ClickUp.SpaceID = v
	}
	if v := os.Getenv("IVY_CLICKUP_LIST_ID"); v != "" {
		cfg.Connectors.ClickUp.ListID = v
	}
	if v := os.Getenv("IVY_CLICKUP_AUTH_MODE"); v != "" {
		cfg.Connectors.ClickUp.AuthMode = v
	}
	if v := os.Getenv("IVY_CLICKUP_PROXY"); v != "" {
		cfg.Connectors.ClickUp.Proxy = v
	}
	if v := os.Getenv("IVY_CLICKUP_AGENT_USERNAME"); v != "" {
		cfg.Connectors.ClickUp.AgentUsername = v
	}
	if cfg.Connectors.ClickUp.AgentUsername == "" {
		cfg.Connectors.ClickUp.AgentUsername = DefaultAgentUsername
	}

	// GitHub App configuration overrides.
	if v := os.Getenv("IVY_GITHUB_ENABLED"); v != "" {
		cfg.Connectors.GitHub.Enabled = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("IVY_GITHUB_APP_ID"); v != "" {
		var id int64
		if _, err := fmt.Sscanf(v, "%d", &id); err == nil {
			cfg.Connectors.GitHub.AppID = id
		}
	}
	if v := os.Getenv("IVY_GITHUB_PRIVATE_KEY_PATH"); v != "" {
		cfg.Connectors.GitHub.PrivateKeyPath = v
	}
	if v := os.Getenv("IVY_GITHUB_PRIVATE_KEY"); v != "" {
		cfg.Connectors.GitHub.PrivateKey = v
	}
	if v := os.Getenv("IVY_GITHUB_BASE_URL"); v != "" {
		cfg.Connectors.GitHub.BaseURL = v
	}
	if v := os.Getenv("IVY_GITHUB_BRANCH_PREFIX"); v != "" {
		cfg.Connectors.GitHub.BranchPrefix = v
	}
	if v := os.Getenv("IVY_GITHUB_PROTECTED_REFS"); v != "" {
		cfg.Connectors.GitHub.ProtectedRefs = v
	}
	if cfg.Connectors.GitHub.BranchPrefix == "" {
		cfg.Connectors.GitHub.BranchPrefix = "agent/"
	}
	if cfg.Connectors.GitHub.ProtectedRefs == "" {
		cfg.Connectors.GitHub.ProtectedRefs = "main,master,release/*"
	}
	// Resolve private key: if raw content is set, use it; otherwise read from file.
	if cfg.Connectors.GitHub.PrivateKey == "" && cfg.Connectors.GitHub.PrivateKeyPath != "" {
		keyData, err := os.ReadFile(cfg.Connectors.GitHub.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("reading GitHub App private key from %s: %w", cfg.Connectors.GitHub.PrivateKeyPath, err)
		}
		cfg.Connectors.GitHub.PrivateKey = string(keyData)
	}
	if v := os.Getenv("IVY_CLICKUP_AUTH_MODE"); v != "" {
		cfg.Connectors.ClickUp.AuthMode = v
	}
	if v := os.Getenv("IVY_EMBEDDING_DIM"); v != "" {
		var dim int
		if _, err := fmt.Sscanf(v, "%d", &dim); err == nil && dim > 0 {
			cfg.LLM.EmbeddingDim = dim
		}
	}

	return cfg, nil
}
