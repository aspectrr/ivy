package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the configuration for leaf.
type Config struct {
	Vine               VineConnConfig `yaml:"vine"`
	AllowedDirectories []string       `yaml:"allowed_directories"`
	AllowedCommands    []string       `yaml:"allowed_commands"`
	MaxConcurrentCmds  int            `yaml:"max_concurrent_commands"`
	CommandTimeout     time.Duration  `yaml:"command_timeout"`
	LogLevel           string         `yaml:"log_level"`
}

type TLSConfig struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
	CA   string `yaml:"ca"`
}

type VineConnConfig struct {
	Address           string        `yaml:"address"`
	TLS               TLSConfig     `yaml:"tls"`
	ReconnectInterval time.Duration `yaml:"reconnect_interval"`
}

// LoadConfig loads the leaf configuration from a YAML file.
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
	if v := os.Getenv("IVY_VINE_ADDRESS"); v != "" {
		cfg.Vine.Address = v
	}

	return cfg, nil
}
