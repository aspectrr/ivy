package config

import (
	"fmt"
	"os"
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
}

type ClickUpConfig struct {
	Enabled       bool          `yaml:"enabled"`
	APIToken      string        `yaml:"api_token"`
	TeamID        string        `yaml:"team_id"`
	ListID        string        `yaml:"list_id"`
	SpaceID       string        `yaml:"space_id"`
	Tag           string        `yaml:"tag"`
	Assignee      string        `yaml:"assignee"`
	PollInterval  time.Duration `yaml:"poll_interval"`
	Proxy         string        `yaml:"proxy"`
	WebhookSecret string        `yaml:"webhook_secret"`
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
	if v := os.Getenv("IVY_DB_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
	if v := os.Getenv("IVY_LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
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
	if v := os.Getenv("IVY_CLICKUP_PROXY"); v != "" {
		cfg.Connectors.ClickUp.Proxy = v
	}

	return cfg, nil
}
