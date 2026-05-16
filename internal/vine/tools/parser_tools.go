package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// parserHostTool is a template for log parser host tools that execute
// read-only commands on remote parser hosts via gRPC to leaf daemons.
// Actual gRPC dispatch will be wired in Phase 4.2.
type parserHostTool struct {
	name        string
	description string
	command     string // e.g. "grep", "awk", "find"
}

func (t *parserHostTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.name,
		Description: t.description,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"args":{"type":"string","description":"Command arguments"},"host":{"type":"string","description":"Parser host ID (optional, uses default if omitted)"}},"required":["args"]}`),
	}
}

func (t *parserHostTool) Execute(_ context.Context, args json.RawMessage, _ ToolContext) (json.RawMessage, error) {
	var params struct {
		Args string `json:"args"`
		Host string `json:"host"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	// TODO: Route through gRPC to leaf daemon (Phase 4.2).
	return json.Marshal(map[string]interface{}{
		"output":  "",
		"message": fmt.Sprintf("parser_%s not yet connected to leaf daemon (Phase 4)", t.command),
	})
}

// RegisterParserTools registers all log parser host tools.
func RegisterParserTools(registry *Registry) error {
	parserTools := []struct {
		name        string
		description string
		command     string
	}{
		{
			name:        "parser_grep",
			description: "Run grep on a log parser host. Only read-only flags allowed. Searches within allowed directories only.",
			command:     "grep",
		},
		{
			name:        "parser_awk",
			description: "Run awk on a log parser host. Basic programs only (no system() or pipe).",
			command:     "awk",
		},
		{
			name:        "parser_find",
			description: "Run find on a log parser host. Searches within allowed directories only.",
			command:     "find",
		},
		{
			name:        "parser_cat",
			description: "Run cat on a log parser host. Only files within allowed directories.",
			command:     "cat",
		},
		{
			name:        "parser_read_file",
			description: "Read a specific file from a log parser host. Path must be within allowed directories.",
			command:     "read_file",
		},
		{
			name:        "parser_tail",
			description: "Run tail on a log parser host. Supports -n flag. -f is timeout-bound.",
			command:     "tail",
		},
		{
			name:        "parser_systemctl_status",
			description: "Check the status of logstash services on a parser host via systemctl status.",
			command:     "systemctl_status",
		},
		{
			name:        "parser_journalctl",
			description: "Query journal logs on a parser host. Read-only flags only (-u, -n, --since, --until, --no-pager).",
			command:     "journalctl",
		},
	}

	for _, pt := range parserTools {
		tool := &parserHostTool{
			name:        pt.name,
			description: pt.description,
			command:     pt.command,
		}
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}
