package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aspectrr/ivy/internal/ivyv1"
)

// LeafManager is the interface parser tools use to route commands to leaf daemons.
type LeafManager interface {
	SendCommandAndWait(ctx context.Context, hostID string, req *ivyv1.ExecuteCommandRequest) (*ivyv1.CommandOutput, error)
}

// parserHostTool executes read-only commands on remote parser hosts via gRPC.
type parserHostTool struct {
	name        string
	description string
	commandType ivyv1.CommandType
	leafMgr     LeafManager
}

func (t *parserHostTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.name,
		Description: t.description,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"args":{"type":"string","description":"Command arguments (space-separated)"},"host":{"type":"string","description":"Parser host ID (optional, uses default if omitted)"}},"required":["args"]}`),
	}
}

func (t *parserHostTool) Execute(ctx context.Context, args json.RawMessage, _ ToolContext) (json.RawMessage, error) {
	var params struct {
		Args string `json:"args"`
		Host string `json:"host"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if t.leafMgr == nil {
		return nil, fmt.Errorf("parser host tools not available: no leaf manager configured")
	}

	// Split args string into individual arguments
	argList := splitArgs(params.Args)

	req := &ivyv1.ExecuteCommandRequest{
		RequestId: fmt.Sprintf("parser-%d", time.Now().UnixNano()),
		Command:   t.commandType,
		Args:      argList,
	}

	output, err := t.leafMgr.SendCommandAndWait(ctx, params.Host, req)
	if err != nil {
		return nil, fmt.Errorf("executing %s on parser host: %w", t.name, err)
	}

	result := map[string]interface{}{
		"exit_code": output.ExitCode,
		"stdout":    output.Stdout,
		"stderr":    output.Stderr,
		"timeout":   output.Timeout,
	}
	return json.Marshal(result)
}

// splitArgs splits a command argument string, respecting quoted substrings.
func splitArgs(s string) []string {
	var args []string
	var current strings.Builder
	inQuote := false

	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == '"' && !inQuote:
			inQuote = true
		case r == '"' && inQuote:
			inQuote = false
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// commandNameToProto maps command string names to proto CommandType.
var commandNameToProto = map[string]ivyv1.CommandType{
	"grep":             ivyv1.CommandType_GREP,
	"awk":              ivyv1.CommandType_AWK,
	"find":             ivyv1.CommandType_FIND,
	"cat":              ivyv1.CommandType_CAT,
	"read_file":        ivyv1.CommandType_READ_FILE,
	"tail":             ivyv1.CommandType_TAIL,
	"systemctl_status": ivyv1.CommandType_SYSTEMCTL_STATUS,
	"journalctl":       ivyv1.CommandType_JOURNALCTL,
}

// RegisterParserTools registers all log parser host tools.
func RegisterParserTools(registry *Registry, leafMgr LeafManager) error {
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
		cmdType, ok := commandNameToProto[pt.command]
		if !ok {
			return fmt.Errorf("unknown command %q for parser tool %s", pt.command, pt.name)
		}

		tool := &parserHostTool{
			name:        pt.name,
			description: pt.description,
			commandType: cmdType,
			leafMgr:     leafMgr,
		}
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}
