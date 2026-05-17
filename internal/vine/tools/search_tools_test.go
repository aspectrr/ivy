package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// mockSkillSearcher is a mock SkillSearcher.
type mockSkillSearcher struct {
	results []SkillSearchResult
	err     error
}

func (m *mockSkillSearcher) SearchByText(_ context.Context, _ string, _ int) ([]SkillSearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

// mockSkillCreator is a mock SkillCreator.
type mockSkillCreator struct {
	err error
}

func (m *mockSkillCreator) Create(_ context.Context, name, desc, content string, _ *string) (*SkillSearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &SkillSearchResult{ID: "new-id", Name: name, Description: desc, Content: content}, nil
}

// mockHistorySearcher is a mock HistorySearcher.
type mockHistorySearcher struct {
	results []HistoryResult
	err     error
}

func (m *mockHistorySearcher) SearchByText(_ context.Context, _ string, _ int) ([]HistoryResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

func mustUnmarshal(t *testing.T, data json.RawMessage, v interface{}) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestSearchSkillsTool_WithMock(t *testing.T) {
	reg := NewRegistry()
	searcher := &mockSkillSearcher{
		results: []SkillSearchResult{
			{ID: "s1", Name: "kafka-debug", Description: "Debug Kafka issues", Content: "Check consumer groups", BuiltIn: true},
			{ID: "s2", Name: "es-query", Description: "ES query patterns", Content: "Use bool queries", BuiltIn: false},
		},
	}
	err := RegisterSearchTools(reg, searcher, nil, nil)
	if err != nil {
		t.Fatalf("RegisterSearchTools: %v", err)
	}

	result, err := reg.Execute(context.Background(), "search_skills",
		json.RawMessage(`{"query":"kafka consumer lag"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var output map[string]interface{}
	mustUnmarshal(t, result, &output)
	if output["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", output["count"])
	}
}

func TestSearchHistoryTool_WithMock(t *testing.T) {
	reg := NewRegistry()
	searcher := &mockHistorySearcher{
		results: []HistoryResult{
			{SessionID: "sess1", Content: "Fixed Kafka lag by rebalancing", CreatedAt: "2024-01-01T00:00:00Z"},
		},
	}
	err := RegisterSearchTools(reg, nil, nil, searcher)
	if err != nil {
		t.Fatalf("RegisterSearchTools: %v", err)
	}

	result, err := reg.Execute(context.Background(), "search_history",
		json.RawMessage(`{"query":"kafka lag"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var output map[string]interface{}
	mustUnmarshal(t, result, &output)
	if output["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1", output["count"])
	}
}

func TestCreateSkillTool_WithMock(t *testing.T) {
	reg := NewRegistry()
	creator := &mockSkillCreator{}
	err := RegisterSearchTools(reg, nil, creator, nil)
	if err != nil {
		t.Fatalf("RegisterSearchTools: %v", err)
	}

	result, err := reg.Execute(context.Background(), "skill_create",
		json.RawMessage(`{"name":"test-skill","description":"A test","content":"Do the thing"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var output map[string]interface{}
	mustUnmarshal(t, result, &output)
	if output["name"] != "test-skill" {
		t.Errorf("name = %v, want test-skill", output["name"])
	}
}

func TestCreateSkillTool_MissingFields(t *testing.T) {
	reg := NewRegistry()
	creator := &mockSkillCreator{}
	_ = RegisterSearchTools(reg, nil, creator, nil)

	_, err := reg.Execute(context.Background(), "skill_create",
		json.RawMessage(`{"name":"test-skill"}`), ToolContext{})
	if err == nil {
		t.Fatal("expected error when content is missing")
	}
}

func TestCreateSkillTool_NotConfigured(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSearchTools(reg, nil, nil, nil)

	_, err := reg.Execute(context.Background(), "skill_create",
		json.RawMessage(`{"name":"test","description":"test","content":"test"}`), ToolContext{})
	if err == nil {
		t.Fatal("expected error when creator is nil")
	}
}

func TestSearchSkillsTool_NotConfigured(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSearchTools(reg, nil, nil, nil)

	result, err := reg.Execute(context.Background(), "search_skills",
		json.RawMessage(`{"query":"test"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var output map[string]interface{}
	mustUnmarshal(t, result, &output)
	msg, _ := output["message"].(string)
	if msg == "" {
		t.Error("expected message in output")
	}
}

func TestSearchHistoryTool_NotConfigured(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSearchTools(reg, nil, nil, nil)

	result, err := reg.Execute(context.Background(), "search_history",
		json.RawMessage(`{"query":"test"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var output map[string]interface{}
	mustUnmarshal(t, result, &output)
	results := output["results"].([]interface{})
	if len(results) != 0 {
		t.Errorf("expected empty results when not configured, got %d", len(results))
	}
}

func TestCreateSkillTool_WithSessionID(t *testing.T) {
	reg := NewRegistry()
	creator := &mockSkillCreator{}
	_ = RegisterSearchTools(reg, nil, creator, nil)

	result, err := reg.Execute(context.Background(), "skill_create",
		json.RawMessage(`{"name":"session-skill","description":"From session","content":"Learned this"}`),
		ToolContext{SessionID: "session-123"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var output map[string]interface{}
	mustUnmarshal(t, result, &output)
	if output["id"] == nil {
		t.Error("expected id in output")
	}
}
