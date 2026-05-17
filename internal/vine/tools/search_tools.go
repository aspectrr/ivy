package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SkillSearcher is the interface for skill search (backed by skills.Store).
type SkillSearcher interface {
	SearchByText(ctx context.Context, query string, limit int) ([]SkillSearchResult, error)
}

// SkillSearchResult represents a found skill.
type SkillSearchResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	BuiltIn     bool   `json:"built_in"`
}

// SkillCreator is the interface for creating skills.
type SkillCreator interface {
	Create(ctx context.Context, name, description, content string, sourceSessionID *string) (*SkillSearchResult, error)
}

// HistorySearcher is the interface for history search (backed by history.Store).
type HistorySearcher interface {
	SearchByText(ctx context.Context, query string, limit int) ([]HistoryResult, error)
}

// HistoryResult represents a found history entry.
type HistoryResult struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Summary   string `json:"summary,omitempty"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
}

// --- search_history ---

type SearchHistoryTool struct {
	Searcher HistorySearcher
}

func (t *SearchHistoryTool) Definition() ToolDef {
	return ToolDef{
		Name:        "search_history",
		Description: "Search past session history for relevant context. Use this when you encounter unfamiliar issues or want to learn from past debugging sessions.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query describing what you're looking for"},"limit":{"type":"integer","description":"Maximum results to return (default 5)","default":5}},"required":["query"]}`),
	}
}

func (t *SearchHistoryTool) Execute(ctx context.Context, args json.RawMessage, _ ToolContext) (json.RawMessage, error) {
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

	if t.Searcher == nil {
		return json.Marshal(map[string]interface{}{
			"results": []interface{}{},
			"message": "history search not configured",
		})
	}

	results, err := t.Searcher.SearchByText(ctx, params.Query, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("searching history: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"results": results,
		"count":   len(results),
	})
}

// --- search_skills ---

type SearchSkillsTool struct {
	Searcher SkillSearcher
}

func (t *SearchSkillsTool) Definition() ToolDef {
	return ToolDef{
		Name:        "search_skills",
		Description: "Search available skills by name, description, or semantic similarity. Skills contain learned patterns from past agent sessions.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query for relevant skills"},"limit":{"type":"integer","description":"Maximum results to return (default 5)","default":5}},"required":["query"]}`),
	}
}

func (t *SearchSkillsTool) Execute(ctx context.Context, args json.RawMessage, _ ToolContext) (json.RawMessage, error) {
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

	if t.Searcher == nil {
		return json.Marshal(map[string]interface{}{
			"results": []interface{}{},
			"message": "skill search not configured",
		})
	}

	results, err := t.Searcher.SearchByText(ctx, params.Query, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("searching skills: %w", err)
	}

	// Format as readable list for the agent
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d skills:\n\n", len(results))
	for _, r := range results {
		badge := ""
		if r.BuiltIn {
			badge = " [built-in]"
		}
		fmt.Fprintf(&b, "- **%s**%s: %s\n", r.Name, badge, r.Description)
	}

	return json.Marshal(map[string]interface{}{
		"results": results,
		"count":   len(results),
		"message": b.String(),
	})
}

// --- skill_create ---

type CreateSkillTool struct {
	Creator SkillCreator
}

func (t *CreateSkillTool) Definition() ToolDef {
	return ToolDef{
		Name:        "skill_create",
		Description: "Create a new skill capturing knowledge from this session. Use at the end of a session to help future agents. Write a clear, actionable skill with steps that worked.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Descriptive skill name, e.g. 'kafka-consumer-lag-investigation'"},"description":{"type":"string","description":"Brief description for searchability"},"content":{"type":"string","description":"Full skill content with steps, commands, and patterns that worked"}},"required":["name","description","content"]}`),
	}
}

func (t *CreateSkillTool) Execute(ctx context.Context, args json.RawMessage, tc ToolContext) (json.RawMessage, error) {
	var params struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parsing args: %w", err)
	}

	if params.Name == "" || params.Content == "" {
		return nil, fmt.Errorf("name and content are required")
	}

	if t.Creator == nil {
		return nil, fmt.Errorf("skill creation not configured")
	}

	var sourceSessionID *string
	if tc.SessionID != "" {
		sourceSessionID = &tc.SessionID
	}

	skill, err := t.Creator.Create(ctx, params.Name, params.Description, params.Content, sourceSessionID)
	if err != nil {
		return nil, fmt.Errorf("creating skill: %w", err)
	}

	return json.Marshal(map[string]interface{}{
		"id":      skill.ID,
		"name":    skill.Name,
		"message": fmt.Sprintf("Skill %q created successfully", skill.Name),
	})
}

// RegisterSearchTools registers all search tools.
// searcher and creator can be nil (tools return "not configured" messages).
func RegisterSearchTools(registry *Registry, skillSearcher SkillSearcher, skillCreator SkillCreator, historySearcher HistorySearcher) error {
	tools := []Tool{
		&SearchHistoryTool{Searcher: historySearcher},
		&SearchSkillsTool{Searcher: skillSearcher},
		&CreateSkillTool{Creator: skillCreator},
	}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}
