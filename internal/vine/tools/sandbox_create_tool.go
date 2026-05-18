package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aspectrr/ivy/internal/ivyv1"
	"github.com/aspectrr/ivy/internal/vine/vine"
)

// SandboxCreator creates pipeline sandboxes with config from production hosts.
type SandboxCreator interface {
	CreatePipeline(ctx context.Context, sessionID string, cfg vine.PipelineConfig) (*vine.PipelineSandbox, error)
}

// LeafCommandRunner runs commands on leaf daemons.
type LeafCommandRunner interface {
	SendCommandAndWait(ctx context.Context, hostID string, req *ivyv1.ExecuteCommandRequest) (*ivyv1.CommandOutput, error)
}

// CreateSandboxTool creates a pipeline sandbox by reading config from a production host.
type CreateSandboxTool struct {
	Creator    SandboxCreator
	LeafRunner LeafCommandRunner
}

func (t *CreateSandboxTool) Definition() ToolDef {
	return ToolDef{
		Name: "create_sandbox",
		Description: "Create a pipeline sandbox (Redpanda + Logstash + Elasticsearch) configured to match a production parser host. " +
			"Reads Logstash config files from the specified host and spins up a matching test environment. " +
			"You MUST call this before using sandbox_bash, sandbox_read_file, sandbox_write_file, or pipeline_* tools. " +
			"Use list_parser_hosts first to discover available hosts.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"host_id": {"type": "string", "description": "Parser host ID to mirror. Use list_parser_hosts to discover available hosts."}
			},
			"required": ["host_id"]
		}`),
	}
}

func (t *CreateSandboxTool) Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	var params struct {
		HostID string `json:"host_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if params.HostID == "" {
		return nil, fmt.Errorf("host_id is required: use list_parser_hosts to discover available hosts")
	}

	// Step 1: Detect Logstash version.
	version, err := t.detectLogstashVersion(ctx, params.HostID)
	if err != nil {
		return nil, fmt.Errorf("detecting Logstash version on host %s: %w", params.HostID, err)
	}

	// Step 2: Find and read Logstash config files.
	config, err := t.readLogstashConfig(ctx, params.HostID)
	if err != nil {
		return nil, fmt.Errorf("reading Logstash config from host %s: %w", params.HostID, err)
	}

	// Step 3: Create the pipeline sandbox.
	image := fmt.Sprintf("docker.elastic.co/logstash/logstash:%s", version)
	_, err = t.Creator.CreatePipeline(ctx, tctx.SessionID, vine.PipelineConfig{
		LogstashImage:  image,
		LogstashConfig: config,
	})
	if err != nil {
		return nil, fmt.Errorf("creating pipeline sandbox: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"status":           "created",
		"logstash_version": version,
		"logstash_image":   image,
		"session_id":       tctx.SessionID,
		"components": map[string]string{
			"redpanda":      "running",
			"elasticsearch": "running",
			"logstash":      "running",
		},
		"message": fmt.Sprintf("Pipeline sandbox created mirroring host %s (Logstash %s). Use pipeline_health to verify all components are ready, then pipeline_send_data to test.", params.HostID, version),
	})
}

// detectLogstashVersion queries the leaf host for the installed Logstash version.
func (t *CreateSandboxTool) detectLogstashVersion(ctx context.Context, hostID string) (string, error) {
	// Try multiple detection methods in order.

	// Method 1: Check the logstash binary version.
	output, err := t.runLeafCommand(ctx, hostID, ivyv1.CommandType_CAT, []string{"/usr/share/logstash/VERSION"})
	if err == nil && output.ExitCode == 0 {
		v := strings.TrimSpace(output.Stdout)
		if v != "" {
			return v, nil
		}
	}

	// Method 2: Check systemctl status which often shows the version.
	output, err = t.runLeafCommand(ctx, hostID, ivyv1.CommandType_SYSTEMCTL_STATUS, []string{"logstash"})
	if err == nil && output.ExitCode == 0 {
		// Look for version in output like "logstash 8.15.0"
		re := regexp.MustCompile(`logstash[\s/]+(\d+\.\d+\.\d+)`)
		if m := re.FindStringSubmatch(output.Stdout); len(m) > 1 {
			return m[1], nil
		}
	}

	// Method 3: Grep for version in logstash-core plugin.
	output, err = t.runLeafCommand(ctx, hostID, ivyv1.CommandType_GREP, []string{"-r", "LOGSTASH_VERSION", "/usr/share/logstash/logstash-core/lib/gem_version.rb"})
	if err == nil && output.ExitCode == 0 {
		re := regexp.MustCompile(`(\d+\.\d+\.\d+)`)
		if m := re.FindStringSubmatch(output.Stdout); len(m) > 1 {
			return m[1], nil
		}
	}

	return "", fmt.Errorf("could not detect Logstash version on host %s — the host may not have Logstash installed or the version file is in an unexpected location", hostID)
}

// readLogstashConfig reads all .conf files from the host's Logstash config directory.
func (t *CreateSandboxTool) readLogstashConfig(ctx context.Context, hostID string) (string, error) {
	// Find config files in common Logstash config locations.
	configDirs := []string{"/etc/logstash/conf.d", "/etc/logstash/pipeline.conf.d", "/etc/logstash"}

	var confFiles []string
	for _, dir := range configDirs {
		output, err := t.runLeafCommand(ctx, hostID, ivyv1.CommandType_FIND, []string{dir, "-name", "*.conf", "-type", "f"})
		if err != nil || output.ExitCode != 0 {
			continue
		}
		for _, line := range strings.Split(output.Stdout, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && strings.HasSuffix(line, ".conf") {
				confFiles = append(confFiles, line)
			}
		}
		if len(confFiles) > 0 {
			break
		}
	}

	if len(confFiles) == 0 {
		return "", fmt.Errorf("no .conf files found in Logstash config directories on host %s", hostID)
	}

	// Read each config file.
	var parts []string
	for _, f := range confFiles {
		output, err := t.runLeafCommand(ctx, hostID, ivyv1.CommandType_CAT, []string{f})
		if err != nil || output.ExitCode != 0 {
			return "", fmt.Errorf("failed to read %s: %w", f, err)
		}
		parts = append(parts, fmt.Sprintf("# --- %s ---\n%s", f, output.Stdout))
	}

	return strings.Join(parts, "\n\n"), nil
}

func (t *CreateSandboxTool) runLeafCommand(ctx context.Context, hostID string, cmdType ivyv1.CommandType, args []string) (*ivyv1.CommandOutput, error) {
	req := &ivyv1.ExecuteCommandRequest{
		RequestId:      fmt.Sprintf("sandbox-%d", time.Now().UnixNano()),
		Command:        cmdType,
		Args:           args,
		TimeoutSeconds: 15,
	}
	return t.LeafRunner.SendCommandAndWait(ctx, hostID, req)
}
