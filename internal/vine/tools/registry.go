package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aspectrr/ivy/internal/vine/eventstore"
	"github.com/aspectrr/ivy/internal/vine/vine"
)

// Tool is the interface that all agent tools must implement.
type Tool interface {
	// Definition returns the tool's JSON schema definition for LLM function calling.
	Definition() ToolDef
	// Execute runs the tool with the given arguments and context.
	Execute(ctx context.Context, args json.RawMessage, tctx ToolContext) (json.RawMessage, error)
}

// ToolDef describes a tool for the LLM's function calling schema.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolContext provides context for a tool execution.
type ToolContext struct {
	SessionID  string
	Sandbox    *vine.Sandbox
	EventStore *eventstore.Store
	// ParserClient will be added in Phase 4 when leaf daemon is built.
}

// Registry manages tool registration and dispatch.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	def := tool.Definition()
	if def.Name == "" {
		return fmt.Errorf("tool has empty name")
	}
	if _, exists := r.tools[def.Name]; exists {
		return fmt.Errorf("tool %q already registered", def.Name)
	}

	r.tools[def.Name] = tool
	return nil
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}
	return tool, nil
}

// List returns all registered tool definitions for LLM function calling.
func (r *Registry) List() []ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDef, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, tool.Definition())
	}
	return defs
}

// Execute dispatches a tool call by name.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage, tctx ToolContext) (json.RawMessage, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("tool %q not found", name)
	}

	return tool.Execute(ctx, args, tctx)
}
