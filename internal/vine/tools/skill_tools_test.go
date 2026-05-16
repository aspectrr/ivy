package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestListSkillsTool(t *testing.T) {
	store := NewMemorySkillStore()
	reg := NewRegistry()
	if err := RegisterSkillTools(reg, store); err != nil {
		t.Fatalf("RegisterSkillTools: %v", err)
	}

	result, err := reg.Execute(context.Background(), "list_skills", json.RawMessage(`{}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute list_skills: %v", err)
	}

	var output map[string]interface{}
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	skillsStr, ok := output["skills"].(string)
	if !ok {
		t.Fatal("expected skills to be a string")
	}
	if !strings.Contains(skillsStr, "kafka-debugging") {
		t.Fatalf("expected kafka-debugging in skills list, got: %s", skillsStr)
	}
	if !strings.Contains(skillsStr, "logstash-config-patterns") {
		t.Fatalf("expected logstash-config-patterns in skills list")
	}
	if !strings.Contains(skillsStr, "built-in") {
		t.Fatalf("expected [built-in] badges in skills list")
	}
	if !strings.Contains(skillsStr, "get_skill") {
		t.Fatalf("expected get_skill hint in output")
	}

	message, _ := output["message"].(string)
	if !strings.Contains(message, "5 skills") {
		t.Fatalf("expected '5 skills' in message, got: %s", message)
	}
}

func TestGetSkillTool(t *testing.T) {
	store := NewMemorySkillStore()
	reg := NewRegistry()
	_ = RegisterSkillTools(reg, store)

	result, err := reg.Execute(context.Background(), "get_skill", json.RawMessage(`{"name":"kafka-debugging"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute get_skill: %v", err)
	}

	var output map[string]string
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if output["name"] != "kafka-debugging" {
		t.Fatalf("expected name=kafka-debugging, got %s", output["name"])
	}
	if output["description"] == "" {
		t.Fatal("expected non-empty description")
	}
	if !strings.Contains(output["content"], "Consumer Lag") {
		t.Fatalf("expected skill content about Consumer Lag, got: %s", output["content"])
	}
}

func TestGetSkillNotFound(t *testing.T) {
	store := NewMemorySkillStore()
	reg := NewRegistry()
	_ = RegisterSkillTools(reg, store)

	_, err := reg.Execute(context.Background(), "get_skill", json.RawMessage(`{"name":"nonexistent"}`), ToolContext{})
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

func TestGetSkillEmptyName(t *testing.T) {
	store := NewMemorySkillStore()
	reg := NewRegistry()
	_ = RegisterSkillTools(reg, store)

	_, err := reg.Execute(context.Background(), "get_skill", json.RawMessage(`{"name":""}`), ToolContext{})
	if err == nil {
		t.Fatal("expected error for empty skill name")
	}
}

func TestSkillToolsWithNilStore(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSkillTools(reg, nil)

	_, err := reg.Execute(context.Background(), "list_skills", json.RawMessage(`{}`), ToolContext{})
	if err == nil {
		t.Fatal("expected error with nil store")
	}

	_, err = reg.Execute(context.Background(), "get_skill", json.RawMessage(`{"name":"test"}`), ToolContext{})
	if err == nil {
		t.Fatal("expected error with nil store")
	}
}

func TestAllBuiltInSkillsLoadable(t *testing.T) {
	store := NewMemorySkillStore()
	reg := NewRegistry()
	_ = RegisterSkillTools(reg, store)

	// List skills first.
	listResult, err := reg.Execute(context.Background(), "list_skills", json.RawMessage(`{}`), ToolContext{})
	if err != nil {
		t.Fatalf("list_skills: %v", err)
	}

	var listOutput map[string]interface{}
	_ = json.Unmarshal(listResult, &listOutput)
	message := listOutput["message"].(string)
	if !strings.Contains(message, "5 skills") {
		t.Fatalf("expected 5 built-in skills, message: %s", message)
	}

	// Load each skill by name.
	expectedSkills := []string{
		"kafka-debugging",
		"elasticsearch-query-patterns",
		"logstash-config-patterns",
		"sysadmin-debugging",
		"create-skill",
	}

	for _, name := range expectedSkills {
		t.Run("get_"+name, func(t *testing.T) {
			result, err := reg.Execute(context.Background(), "get_skill", json.RawMessage(`{"name":"`+name+`"}`), ToolContext{})
			if err != nil {
				t.Fatalf("get_skill %s: %v", name, err)
			}

			var output map[string]string
			_ = json.Unmarshal(result, &output)
			if output["name"] != name {
				t.Fatalf("expected name=%s, got %s", name, output["name"])
			}
			if len(output["content"]) < 50 {
				t.Fatalf("skill %s has suspiciously short content: %q", name, output["content"])
			}
		})
	}
}

func TestSkillToolsRegistered(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterSkillTools(reg, NewMemorySkillStore())

	defs := reg.List()
	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Name] = true
	}

	if !names["list_skills"] {
		t.Fatal("expected list_skills to be registered")
	}
	if !names["get_skill"] {
		t.Fatal("expected get_skill to be registered")
	}

	// Verify tool definitions are valid.
	for _, name := range []string{"list_skills", "get_skill"} {
		tool, _ := reg.Get(name)
		def := tool.Definition()
		if def.Description == "" {
			t.Fatalf("%s has empty description", name)
		}
		if len(def.Parameters) == 0 {
			t.Fatalf("%s has empty parameters", name)
		}
	}
}
