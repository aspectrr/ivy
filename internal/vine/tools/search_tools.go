package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- search_history ---

type SearchHistoryTool struct{}

func (t *SearchHistoryTool) Definition() ToolDef {
	return ToolDef{
		Name:        "search_history",
		Description: "Search past session history for relevant context. Use this when you encounter unfamiliar issues or want to learn from past debugging sessions.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query describing what you're looking for"},"limit":{"type":"integer","description":"Maximum results to return (default 5)","default":5}},"required":["query"]}`),
	}
}

func (t *SearchHistoryTool) Execute(_ context.Context, args json.RawMessage, _ ToolContext) (json.RawMessage, error) {
	var params struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if params.Limit <= 0 {
		params.Limit = 5
	}

	// TODO: Implement actual history search (Phase 5.3 — vector + structured search).
	return json.Marshal(map[string]interface{}{
		"results": []interface{}{},
		"message": fmt.Sprintf("history search for %q not yet implemented (Phase 5.3)", params.Query),
	})
}

// --- search_skills ---

type SearchSkillsTool struct{}

func (t *SearchSkillsTool) Definition() ToolDef {
	return ToolDef{
		Name:        "search_skills",
		Description: "Search available skills by name, description, or semantic similarity. Skills contain learned patterns from past agent sessions.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query for relevant skills"},"limit":{"type":"integer","description":"Maximum results to return (default 5)","default":5}},"required":["query"]}`),
	}
}

func (t *SearchSkillsTool) Execute(_ context.Context, args json.RawMessage, _ ToolContext) (json.RawMessage, error) {
	var params struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if params.Limit <= 0 {
		params.Limit = 5
	}

	// TODO: Implement actual skill search (Phase 5.2 — pgvector similarity search).
	return json.Marshal(map[string]interface{}{
		"results": []interface{}{},
		"message": fmt.Sprintf("skill search for %q not yet implemented (Phase 5.2)", params.Query),
	})
}

// RegisterSearchTools registers all search tools.
func RegisterSearchTools(registry *Registry) error {
	tools := []Tool{
		&SearchHistoryTool{},
		&SearchSkillsTool{},
	}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}
