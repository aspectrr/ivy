package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- sandbox_bash ---

type SandboxBashTool struct{}

func (t *SandboxBashTool) Definition() ToolDef {
	return ToolDef{
		Name:        "sandbox_bash",
		Description: "Execute a bash command in the agent workspace sandbox. The sandbox is an isolated container with no network access.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"The bash command to execute"}},"required":["command"]}`),
	}
}

func (t *SandboxBashTool) Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	if tctx.Sandbox == nil {
		return nil, fmt.Errorf("no sandbox available")
	}

	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	result, err := tctx.Sandbox.Exec(ctx, "bash", "-c", params.Command)
	if err != nil {
		return nil, fmt.Errorf("executing command: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	})
}

// --- sandbox_read_file ---

type SandboxReadFileTool struct{}

func (t *SandboxReadFileTool) Definition() ToolDef {
	return ToolDef{
		Name:        "sandbox_read_file",
		Description: "Read a file from the agent workspace sandbox.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Absolute path to the file in the sandbox"}},"required":["path"]}`),
	}
}

func (t *SandboxReadFileTool) Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	if tctx.Sandbox == nil {
		return nil, fmt.Errorf("no sandbox available")
	}

	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	content, err := tctx.Sandbox.ReadFile(ctx, params.Path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return json.Marshal(map[string]string{"content": content})
}

// --- sandbox_write_file ---

type SandboxWriteFileTool struct{}

func (t *SandboxWriteFileTool) Definition() ToolDef {
	return ToolDef{
		Name:        "sandbox_write_file",
		Description: "Write content to a file in the agent workspace sandbox.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Absolute path to the file in the sandbox"},"content":{"type":"string","description":"The content to write"}},"required":["path","content"]}`),
	}
}

func (t *SandboxWriteFileTool) Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	if tctx.Sandbox == nil {
		return nil, fmt.Errorf("no sandbox available")
	}

	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if err := tctx.Sandbox.WriteFile(ctx, params.Path, []byte(params.Content)); err != nil {
		return nil, fmt.Errorf("writing file: %w", err)
	}

	return json.Marshal(map[string]string{"status": "ok"})
}

// RegisterSandboxTools registers all workspace sandbox tools.
func RegisterSandboxTools(registry *Registry) error {
	tools := []Tool{
		&SandboxBashTool{},
		&SandboxReadFileTool{},
		&SandboxWriteFileTool{},
	}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}
