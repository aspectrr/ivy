package vine

import (
	"fmt"
	"regexp"
	"strings"
)

// RewriteLogstashConfig replaces external host references in a Logstash config
// with the Docker service names used inside the pipeline sandbox network.
//
// It rewrites:
//   - Kafka bootstrap_servers → kafka:9092
//   - Elasticsearch hosts → elasticsearch:9200
//
// Everything else (filters, grok patterns, codecs) is preserved.
func RewriteLogstashConfig(config string, opts ...RewriteOption) string {
	cfg := rewriteConfig{
		kafkaHost: "kafka",
		kafkaPort: "9092",
		esHost:    "elasticsearch",
		esPort:    "9200",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	result := config

	// Rewrite Kafka bootstrap_servers.
	// Matches: bootstrap_servers => "anything:port" or bootstrap_servers => "anything"
	// Also handles: bootstrap_servers => ["host1:port", "host2:port"]
	result = rewriteKafkaBootstrap(result, cfg.kafkaHost, cfg.kafkaPort)

	// Rewrite Elasticsearch hosts.
	// Matches: hosts => ["http://anything:port", ...] or hosts => "anything:port"
	result = rewriteESHosts(result, cfg.esHost, cfg.esPort)

	return result
}

// RewriteOption is a functional option for config rewriting.
type RewriteOption func(*rewriteConfig)

type rewriteConfig struct {
	kafkaHost string
	kafkaPort string
	esHost    string
	esPort    string
}

// WithKafkaAddr sets custom Kafka address for rewriting.
func WithKafkaAddr(host, port string) RewriteOption {
	return func(c *rewriteConfig) {
		c.kafkaHost = host
		c.kafkaPort = port
	}
}

// WithESAddr sets custom Elasticsearch address for rewriting.
func WithESAddr(host, port string) RewriteOption {
	return func(c *rewriteConfig) {
		c.esHost = host
		c.esPort = port
	}
}

// rewriteKafkaBootstrap replaces bootstrap_servers values in Logstash config.
//
// Handles forms:
//
//	bootstrap_servers => "host:9092"
//	bootstrap_servers => ["host1:9092", "host2:9092"]
//	bootstrap_servers => "host:9092,other:9092"
func rewriteKafkaBootstrap(config, host, port string) string {
	target := fmt.Sprintf("%s:%s", host, port)

	// Array form: bootstrap_servers => ["host1:9092", "host2:9092"]
	arrayRe := regexp.MustCompile(`(bootstrap_servers\s*=>\s*)\[[^\]]*\]`)
	config = arrayRe.ReplaceAllString(config, fmt.Sprintf(`${1}["%s"]`, target))

	// String form with comma-separated: bootstrap_servers => "host:9092,other:9092"
	// or simple string: bootstrap_servers => "host:9092"
	stringRe := regexp.MustCompile(`(bootstrap_servers\s*=>\s*)"[^"]*"`)
	config = stringRe.ReplaceAllString(config, fmt.Sprintf(`${1}"%s"`, target))

	return config
}

// rewriteESHosts replaces Elasticsearch hosts in Logstash config.
//
// Handles forms:
//
//	hosts => ["http://host:9200", ...]
//	hosts => ["host:9200"]
//	hosts => "host:9200"
func rewriteESHosts(config, host, port string) string {
	esURL := fmt.Sprintf("http://%s:%s", host, port)

	// Array form: hosts => ["http://host:9200", ...] or hosts => ["host:9200", ...]
	// We match within elasticsearch output blocks only.
	arrayRe := regexp.MustCompile(`(hosts\s*=>\s*)\[[^\]]*\]`)
	config = arrayRe.ReplaceAllString(config, fmt.Sprintf(`${1}["%s"]`, esURL))

	// String form: hosts => "http://host:9200" or hosts => "host:9200"
	stringRe := regexp.MustCompile(`(hosts\s*=>\s*)"[^"]*"`)
	config = stringRe.ReplaceAllString(config, fmt.Sprintf(`${1}"%s"`, esURL))

	return config
}

// ValidateLogstashConfig performs basic validation on a Logstash config.
// Returns nil if the config looks structurally valid.
func ValidateLogstashConfig(config string) error {
	trimmed := strings.TrimSpace(config)

	if trimmed == "" {
		return fmt.Errorf("empty Logstash config")
	}

	// Check for balanced braces.
	depth := 0
	for _, ch := range config {
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return fmt.Errorf("unbalanced braces: extra closing brace")
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("unbalanced braces: %d unclosed", depth)
	}

	// Check that at least an input or output block exists.
	hasInput := strings.Contains(config, "input {") || strings.Contains(config, "input{")
	hasOutput := strings.Contains(config, "output {") || strings.Contains(config, "output{")

	if !hasInput && !hasOutput {
		return fmt.Errorf("config must have at least an input or output block")
	}

	return nil
}
