package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aspectrr/ivy/internal/ivyv1"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := NewRegistry()

	tool := &SearchHistoryTool{}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("Register: %v", err)
	}

	found, err := reg.Get("search_history")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	def := found.Definition()
	if def.Name != "search_history" {
		t.Fatalf("expected name=search_history, got %s", def.Name)
	}
}

func TestRegistryDuplicate(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(&SearchHistoryTool{}); err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	if err := reg.Register(&SearchHistoryTool{}); err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	reg := NewRegistry()

	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestRegistryList(t *testing.T) {
	reg := NewRegistry()

	_ = reg.Register(&SearchHistoryTool{})
	_ = reg.Register(&SearchSkillsTool{})

	defs := reg.List()
	if len(defs) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(defs))
	}

	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Name] = true
	}
	if !names["search_history"] || !names["search_skills"] {
		t.Fatalf("expected search_history and search_skills, got %v", names)
	}
}

func TestRegistryExecute(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(&SearchHistoryTool{})

	result, err := reg.Execute(context.Background(), "search_history", json.RawMessage(`{"query":"kafka"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var output map[string]interface{}
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	results, ok := output["results"].([]interface{})
	if !ok {
		t.Fatal("expected results array")
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results (not configured), got %d", len(results))
	}
}

func TestRegistryExecuteNotFound(t *testing.T) {
	reg := NewRegistry()

	_, err := reg.Execute(context.Background(), "nonexistent", nil, ToolContext{})
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestRegisterSandboxTools(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterSandboxTools(reg); err != nil {
		t.Fatalf("RegisterSandboxTools: %v", err)
	}

	expected := []string{"sandbox_bash", "sandbox_read_file", "sandbox_write_file"}
	for _, name := range expected {
		if _, err := reg.Get(name); err != nil {
			t.Fatalf("expected tool %s: %v", name, err)
		}
	}
}

func TestRegisterSearchTools(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterSearchTools(reg, nil, nil, nil); err != nil {
		t.Fatalf("RegisterSearchTools: %v", err)
	}

	expected := []string{"search_history", "search_skills", "skill_create"}
	for _, name := range expected {
		if _, err := reg.Get(name); err != nil {
			t.Fatalf("expected tool %s: %v", name, err)
		}
	}
}

func TestRegisterParserTools(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterParserTools(reg, nil); err != nil {
		t.Fatalf("RegisterParserTools: %v", err)
	}

	expected := []string{
		"parser_grep", "parser_awk", "parser_find", "parser_cat",
		"parser_read_file", "parser_tail", "parser_systemctl_status", "parser_journalctl",
		"list_parser_hosts",
	}
	for _, name := range expected {
		if _, err := reg.Get(name); err != nil {
			t.Fatalf("expected tool %s: %v", name, err)
		}
	}
}

func TestRegisterAllTools(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterSandboxTools(reg); err != nil {
		t.Fatalf("sandbox: %v", err)
	}
	if err := RegisterSearchTools(reg, nil, nil, nil); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := RegisterParserTools(reg, nil); err != nil {
		t.Fatalf("parser: %v", err)
	}

	defs := reg.List()
	if len(defs) != 15 { // 3 sandbox + 3 search + 8 parser + 1 leaf_hosts
		t.Fatalf("expected 15 total tools, got %d", len(defs))
	}
}

func TestToolDefinitionsHaveSchema(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSandboxTools(reg)
	_ = RegisterSearchTools(reg, nil, nil, nil)
	_ = RegisterParserTools(reg, nil)

	for _, def := range reg.List() {
		if def.Name == "" {
			t.Fatal("tool has empty name")
		}
		if def.Description == "" {
			t.Fatalf("tool %s has empty description", def.Name)
		}
		if len(def.Parameters) == 0 {
			t.Fatalf("tool %s has empty parameters", def.Name)
		}

		// Parameters should be valid JSON.
		var parsed interface{}
		if err := json.Unmarshal(def.Parameters, &parsed); err != nil {
			t.Fatalf("tool %s has invalid parameters JSON: %v", def.Name, err)
		}
	}
}

func TestSearchToolsDefaultLimit(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSearchTools(reg, nil, nil, nil)

	// Test with no limit specified.
	result, err := reg.Execute(context.Background(), "search_history", json.RawMessage(`{"query":"test"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestListParserHostsTool(t *testing.T) {
	reg := NewRegistry()
	mockLeaf := &mockLeafManager{
		hosts: []LeafHostInfo{
			{HostID: "host-001", Hostname: "parser-01", AllowedDirs: []string{"/etc/logstash"}},
			{HostID: "host-002", Hostname: "parser-02", AllowedDirs: []string{"/etc/logstash", "/var/log"}},
		},
	}
	if err := RegisterParserTools(reg, mockLeaf); err != nil {
		t.Fatalf("RegisterParserTools: %v", err)
	}

	result, err := reg.Execute(context.Background(), "list_parser_hosts", json.RawMessage(`{}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute list_parser_hosts: %v", err)
	}

	var output map[string]interface{}
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	count := int(output["count"].(float64))
	if count != 2 {
		t.Fatalf("expected 2 hosts, got %d", count)
	}

	hosts, ok := output["hosts"].([]interface{})
	if !ok {
		t.Fatal("expected hosts array")
	}
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}

	first := hosts[0].(map[string]interface{})
	if first["host_id"] != "host-001" {
		t.Fatalf("expected host_id=host-001, got %v", first["host_id"])
	}
	if first["hostname"] != "parser-01" {
		t.Fatalf("expected hostname=parser-01, got %v", first["hostname"])
	}
}

func TestListParserHostsToolNoManager(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterParserTools(reg, nil); err != nil {
		t.Fatalf("RegisterParserTools: %v", err)
	}

	_, err := reg.Execute(context.Background(), "list_parser_hosts", json.RawMessage(`{}`), ToolContext{})
	if err == nil {
		t.Fatal("expected error when no leaf manager configured")
	}
}

// mockLeafManager satisfies the LeafManager interface for testing.
type mockLeafManager struct {
	hosts []LeafHostInfo
}

func (m *mockLeafManager) SendCommandAndWait(_ context.Context, _ string, _ *ivyv1.ExecuteCommandRequest) (*ivyv1.CommandOutput, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLeafManager) ListConnectedLeaves() []LeafHostInfo {
	return m.hosts
}

func TestSandboxToolsWithoutSandbox(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSandboxTools(reg)

	tools := []string{"sandbox_bash", "sandbox_read_file", "sandbox_write_file"}
	for _, name := range tools {
		_, err := reg.Execute(context.Background(), name, json.RawMessage(`{"command":"ls","path":"/tmp"}`), ToolContext{})
		if err == nil {
			t.Fatalf("expected error for %s without sandbox", name)
		}
	}
}
