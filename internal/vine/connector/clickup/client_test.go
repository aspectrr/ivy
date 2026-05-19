package clickup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
)

// Simpler approach: just test against a real httptest server
// by making the client use a custom doRequest path.

func TestClient_GetTask(t *testing.T) {
	taskJSON := `{
		"id": "abc123",
		"name": "Fix Kafka consumer lag",
		"status": {"status": "in progress", "color": "#yellow", "type": "custom"},
		"description": "Consumer group xyz is falling behind",
		"date_created": "1700000000000",
		"date_updated": "1700001000000",
		"creator": {"id": 1, "username": "admin"},
		"assignees": [{"id": 2, "username": "engineer"}],
		"tags": [{"name": "kafka"}],
		"url": "https://app.clickup.com/task/abc123"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "pk_test_token" {
			t.Errorf("expected Authorization header pk_test_token, got %s", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v2/task/abc123" {
			t.Errorf("expected path /api/v2/task/abc123, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, taskJSON)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	task, err := client.GetTask(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if task.ID != "abc123" {
		t.Errorf("task.ID = %q, want abc123", task.ID)
	}
	if task.Name != "Fix Kafka consumer lag" {
		t.Errorf("task.Name = %q, want 'Fix Kafka consumer lag'", task.Name)
	}
	if task.Status.Status != "in progress" {
		t.Errorf("task.Status.Status = %q, want 'in progress'", task.Status.Status)
	}
	if len(task.Assignees) != 1 || task.Assignees[0].Username != "engineer" {
		t.Errorf("task.Assignees = %+v, want one assignee 'engineer'", task.Assignees)
	}
	if len(task.Tags) != 1 || task.Tags[0].Name != "kafka" {
		t.Errorf("task.Tags = %+v, want one tag 'kafka'", task.Tags)
	}
}

func TestClient_GetTeamTasks(t *testing.T) {
	tasksJSON := `{
		"tasks": [
			{"id": "t1", "name": "Task 1", "status": {"status": "open"}, "date_created": "1700000000000", "date_updated": "1700001000000"},
			{"id": "t2", "name": "Task 2", "status": {"status": "in progress"}, "date_created": "1700000001000", "date_updated": "1700002000000"}
		],
		"last_page": true
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/team/team123/task") {
			t.Errorf("expected team tasks path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, tasksJSON)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	tasks, err := client.GetTeamTasks(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetTeamTasks() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != "t1" {
		t.Errorf("tasks[0].ID = %q, want t1", tasks[0].ID)
	}
}

func TestClient_UpdateTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/v2/task/abc123" {
			t.Errorf("expected path /api/v2/task/abc123, got %s", r.URL.Path)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["status"] != "done" {
			t.Errorf("expected status=done, got %v", body["status"])
		}

		_, _ = fmt.Fprintf(w, `{"id":"abc123","name":"Fixed","status":{"status":"done"},"date_created":"1700000000000","date_updated":"1700003000000"}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	task, err := client.UpdateTask(context.Background(), "abc123", map[string]interface{}{"status": "done"})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	if task.Status.Status != "done" {
		t.Errorf("task status = %q, want done", task.Status.Status)
	}
}

func TestClient_PostComment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/task/abc123/comment") {
			t.Errorf("expected comment path, got %s", r.URL.Path)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["comment_text"] != "Found the issue" {
			t.Errorf("expected comment_text='Found the issue', got %q", body["comment_text"])
		}

		_, _ = fmt.Fprintf(w, `{"id":90000000000001,"task_id":123,"comment_text":"Found the issue","date":"1700003000000"}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	comment, err := client.PostComment(context.Background(), "abc123", "Found the issue")
	if err != nil {
		t.Fatalf("PostComment() error = %v", err)
	}
	if comment.CommentText != "Found the issue" {
		t.Errorf("comment text = %q, want 'Found the issue'", comment.CommentText)
	}
}

func TestClient_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `{"err_code":"not_found","message":"Task not found"}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	_, err := client.GetTask(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	apiErr := &APIError{}
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("status code = %d, want 404", apiErr.StatusCode)
	}
}

func TestClient_RetryOn429(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprintf(w, `{"err_code":"rate_limited","message":"Too many requests"}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":"abc123","name":"Success","status":{"status":"open"},"date_created":"1700000000000","date_updated":"1700000000000"}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	task, err := client.GetTask(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if task.Name != "Success" {
		t.Errorf("task.Name = %q, want Success", task.Name)
	}
	if callCount != 3 {
		t.Errorf("callCount = %d, want 3 (1 initial + 2 retries)", callCount)
	}
}

func TestClient_NoRetryOn4xx(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `{"err_code":"forbidden","message":"Access denied"}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	_, err := client.GetTask(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (no retries for 403)", callCount)
	}
}

func TestNewClient_WithProxy(t *testing.T) {
	cfg := config.ClickUpConfig{
		APIToken: "pk_test",
		TeamID:   "team1",
		Proxy:    "http://proxy.corp.internal:3128",
	}
	client, err := NewClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.token != "pk_test" {
		t.Errorf("token = %q, want pk_test", client.token)
	}
	if client.teamID != "team1" {
		t.Errorf("teamID = %q, want team1", client.teamID)
	}
}

func TestNewClient_NoProxy(t *testing.T) {
	cfg := config.ClickUpConfig{
		APIToken: "pk_test",
		TeamID:   "team1",
	}
	client, err := NewClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClient_NoToken(t *testing.T) {
	cfg := config.ClickUpConfig{TeamID: "team1"}
	_, err := NewClient(cfg, slog.Default())
	if err == nil {
		t.Fatal("expected error for missing api_token")
	}
}

func TestNewClient_BadProxyURL(t *testing.T) {
	cfg := config.ClickUpConfig{
		APIToken: "pk_test",
		Proxy:    "://invalid",
	}
	_, err := NewClient(cfg, slog.Default())
	if err == nil {
		t.Fatal("expected error for invalid proxy URL")
	}
}

func TestTaskListOpts_Query(t *testing.T) {
	tests := []struct {
		name     string
		opts     *TaskListOpts
		contains []string
		empty    bool
	}{
		{
			name:  "nil opts",
			empty: true,
		},
		{
			name:  "empty opts",
			opts:  &TaskListOpts{},
			empty: true,
		},
		{
			name:     "with order and reverse",
			opts:     &TaskListOpts{OrderBy: "updated", Reverse: true},
			contains: []string{"order_by=updated", "reverse=true"},
		},
		{
			name:     "with filters",
			opts:     &TaskListOpts{ListIDs: []string{"l1"}, Tags: []string{"kafka"}, Statuses: []string{"open"}},
			contains: []string{"list_ids%5B%5D=l1", "tags%5B%5D=kafka", "statuses%5B%5D=open"},
		},
		{
			name:     "with date_updated_gt",
			opts:     &TaskListOpts{DateUpdatedGT: 1700001000000},
			contains: []string{"date_updated_gt=1700001000000"},
		},
		{
			name:     "with subtasks and include_closed",
			opts:     &TaskListOpts{Subtasks: true, IncludeClosed: true},
			contains: []string{"subtasks=true", "include_closed=true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.opts.Query()
			if tt.empty {
				if q != "" {
					t.Errorf("expected empty query, got %q", q)
				}
				return
			}
			for _, s := range tt.contains {
				if !strings.Contains(q, s) {
					t.Errorf("query %q should contain %q", q, s)
				}
			}
		})
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1700000000000", 1700000000000},
		{"0", 0},
		{"", 0},
		{"abc", 0},
	}
	for _, tt := range tests {
		got := parseTimestamp(tt.input)
		if got != tt.want {
			t.Errorf("parseTimestamp(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestClient_GetComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"comments":[{"id":90000000000001,"task_id":123,"comment_text":"First comment","date":"1700000000000"},{"id":90000000000002,"task_id":123,"comment_text":"Second comment","date":"1700001000000"}]}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	comments, err := client.GetComments(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetComments() error = %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].CommentText != "First comment" {
		t.Errorf("comments[0].CommentText = %q", comments[0].CommentText)
	}
}

func TestClient_GetAttachments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"attachments":[{"id":"a1","task_id":"abc123","title":"logstash.conf","url":"https://example.com/file","mime_type":"text/plain","size":1024}]}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	attachments, err := client.GetAttachments(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetAttachments() error = %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Title != "logstash.conf" {
		t.Errorf("attachment title = %q, want logstash.conf", attachments[0].Title)
	}
	if attachments[0].Size != 1024 {
		t.Errorf("attachment size = %d, want 1024", attachments[0].Size)
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		_, _ = fmt.Fprintf(w, `{}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test_token",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.GetTask(ctx, "abc123")
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}
}
