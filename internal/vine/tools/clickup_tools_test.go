package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aspectrr/ivy/internal/vine/connector/clickup"
)

// mockClickUpClient is a mock implementation of ClickUpClient.
type mockClickUpClient struct {
	task        *clickup.Task
	tasks       []clickup.Task
	comment     *clickup.Comment
	comments    []clickup.Comment
	attachments []clickup.Attachment
	err         error
}

func (m *mockClickUpClient) GetTask(_ context.Context, _ string) (*clickup.Task, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.task, nil
}

func (m *mockClickUpClient) UpdateTask(_ context.Context, _ string, _ map[string]interface{}) (*clickup.Task, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.task, nil
}

func (m *mockClickUpClient) PostComment(_ context.Context, _ string, _ string) (*clickup.Comment, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.comment, nil
}

func (m *mockClickUpClient) ReplyToComment(_ context.Context, _ json.Number, _ string) (*clickup.Comment, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.comment, nil
}

func (m *mockClickUpClient) GetComments(_ context.Context, _ string) ([]clickup.Comment, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.comments, nil
}

func (m *mockClickUpClient) GetAttachments(_ context.Context, _ string) ([]clickup.Attachment, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.attachments, nil
}

func (m *mockClickUpClient) GetTeamTasks(_ context.Context, _ *clickup.TaskListOpts) ([]clickup.Task, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tasks, nil
}

func TestRegisterClickUpTools(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterClickUpTools(reg, nil); err != nil {
		t.Fatalf("RegisterClickUpTools: %v", err)
	}

	expected := []string{
		"clickup_get_task",
		"clickup_update_task",
		"clickup_post_comment",
		"clickup_search_tasks",
		"clickup_get_attachments",
	}
	for _, name := range expected {
		if _, err := reg.Get(name); err != nil {
			t.Fatalf("expected tool %s: %v", name, err)
		}
	}
}

func TestClickUpTools_NoClient(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterClickUpTools(reg, nil)

	tools := []string{
		"clickup_get_task",
		"clickup_update_task",
		"clickup_post_comment",
		"clickup_search_tasks",
		"clickup_get_attachments",
	}
	for _, name := range tools {
		_, err := reg.Execute(context.Background(), name, json.RawMessage(`{"task_id":"t1"}`), ToolContext{})
		if err == nil {
			t.Fatalf("expected error for %s without client", name)
		}
	}
}

func TestClickUpGetTask_Execute(t *testing.T) {
	reg := NewRegistry()
	mock := &mockClickUpClient{
		task: &clickup.Task{
			ID:     "t1",
			Name:   "Fix Kafka lag",
			Status: clickup.Status{Status: "in progress"},
		},
	}
	_ = RegisterClickUpTools(reg, mock)

	result, err := reg.Execute(context.Background(), "clickup_get_task", json.RawMessage(`{"task_id":"t1"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var task map[string]interface{}
	if err := json.Unmarshal(result, &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if task["id"] != "t1" {
		t.Errorf("task id = %v, want t1", task["id"])
	}
	if task["name"] != "Fix Kafka lag" {
		t.Errorf("task name = %v, want 'Fix Kafka lag'", task["name"])
	}
}

func TestClickUpUpdateTask_Execute(t *testing.T) {
	reg := NewRegistry()
	mock := &mockClickUpClient{
		task: &clickup.Task{
			ID:     "t1",
			Name:   "Fixed",
			Status: clickup.Status{Status: "done"},
		},
	}
	_ = RegisterClickUpTools(reg, mock)

	result, err := reg.Execute(context.Background(), "clickup_update_task",
		json.RawMessage(`{"task_id":"t1","status":"done"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var task map[string]interface{}
	if err := json.Unmarshal(result, &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if task["id"] != "t1" {
		t.Errorf("task id = %v, want t1", task["id"])
	}
}

func TestClickUpUpdateTask_NoFields(t *testing.T) {
	reg := NewRegistry()
	mock := &mockClickUpClient{}
	_ = RegisterClickUpTools(reg, mock)

	_, err := reg.Execute(context.Background(), "clickup_update_task",
		json.RawMessage(`{"task_id":"t1"}`), ToolContext{})
	if err == nil {
		t.Fatal("expected error when no fields to update")
	}
}

func TestClickUpPostComment_Execute(t *testing.T) {
	reg := NewRegistry()
	mock := &mockClickUpClient{
		comment: &clickup.Comment{
			ID:          json.Number("90140206770907"),
			TaskID:      json.Number("90140206770900"),
			CommentText: "Found the issue",
		},
	}
	_ = RegisterClickUpTools(reg, mock)

	result, err := reg.Execute(context.Background(), "clickup_post_comment",
		json.RawMessage(`{"task_id":"t1","text":"Found the issue"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var comment map[string]interface{}
	if err := json.Unmarshal(result, &comment); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// json.Number marshals as a number; large numbers become float64 when unmarshaled into map[string]interface{}
	idFloat, _ := comment["id"].(float64)
	id := fmt.Sprintf("%.0f", idFloat)
	if id != "90140206770907" {
		t.Errorf("comment id = %v, want 90140206770907", id)
	}
}

func TestClickUpSearchTasks_Execute(t *testing.T) {
	reg := NewRegistry()
	mock := &mockClickUpClient{
		tasks: []clickup.Task{
			{ID: "t1", Name: "Task 1", Status: clickup.Status{Status: "open"}, URL: "https://app.clickup.com/t1"},
			{ID: "t2", Name: "Task 2", Status: clickup.Status{Status: "in progress"}, URL: "https://app.clickup.com/t2"},
		},
	}
	_ = RegisterClickUpTools(reg, mock)

	result, err := reg.Execute(context.Background(), "clickup_search_tasks",
		json.RawMessage(`{"statuses":["open","in progress"]}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}
}

func TestClickUpGetAttachments_Execute(t *testing.T) {
	reg := NewRegistry()
	mock := &mockClickUpClient{
		attachments: []clickup.Attachment{
			{ID: "a1", Title: "config.conf", Size: 2048},
		},
	}
	_ = RegisterClickUpTools(reg, mock)

	result, err := reg.Execute(context.Background(), "clickup_get_attachments",
		json.RawMessage(`{"task_id":"t1"}`), ToolContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1", resp["count"])
	}
}

func TestClickUpToolDefinitions(t *testing.T) {
	reg := NewRegistry()
	_ = RegisterClickUpTools(reg, nil)

	for _, name := range []string{
		"clickup_get_task", "clickup_update_task", "clickup_post_comment",
		"clickup_search_tasks", "clickup_get_attachments",
	} {
		tool, err := reg.Get(name)
		if err != nil {
			t.Fatalf("expected tool %s: %v", name, err)
		}
		def := tool.Definition()
		if def.Name == "" {
			t.Fatalf("tool has empty name")
		}
		if def.Description == "" {
			t.Fatalf("tool %s has empty description", def.Name)
		}
		if len(def.Parameters) == 0 {
			t.Fatalf("tool %s has empty parameters", def.Name)
		}

		var parsed interface{}
		if err := json.Unmarshal(def.Parameters, &parsed); err != nil {
			t.Fatalf("tool %s has invalid parameters JSON: %v", def.Name, err)
		}
	}
}
