package clickup

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
)

func TestPoller_ProcessesTasks(t *testing.T) {
	pollCount := atomic.Int32{}
	handlerCalls := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollCount.Add(1)
		if pollCount.Load() == 1 {
			_, _ = fmt.Fprintf(w, `{"tasks":[{"id":"t1","name":"Task 1","status":{"status":"open"},"date_created":"1700000000000","date_updated":"1700001000000"},{"id":"t2","name":"Task 2","status":{"status":"open"},"date_created":"1700000001000","date_updated":"1700002000000"}],"last_page":true}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"tasks":[],"last_page":true}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	handler := func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {
		handlerCalls.Add(1)
	}

	cfg := config.ClickUpConfig{
		TeamID:       "team123",
		PollInterval: 50 * time.Millisecond,
	}

	poller := NewPoller(client, cfg, handler, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	poller.Start(ctx)

	time.Sleep(200 * time.Millisecond)

	if got := handlerCalls.Load(); got != 2 {
		t.Errorf("handler called %d times, want 2", got)
	}
}

func TestPoller_DetectsMention(t *testing.T) {
	var capturedTask *Task
	var capturedReason TriggerReason
	var capturedMention *MentionInfo

	// Use a fixed "now" so we can place comments just after it
	now := time.Now()
	futureMs := now.Add(1 * time.Hour).UnixMilli() // comment timestamp in the future
	taskUpdatedMs := now.Add(30 * time.Minute).UnixMilli()
	taskCreatedMs := now.Add(-1 * time.Hour).UnixMilli()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Return a task updated after poller start
		if contains(path, "/team/team123/task") {
			_, _ = fmt.Fprintf(w, `{"tasks":[{"id":"t1","name":"Mention Task","status":{"status":"open"},"date_created":"%d","date_updated":"%d"}],"last_page":true}`,
				taskCreatedMs, taskUpdatedMs)
			return
		}

		// Return comments with @mention (new) and old comment
		if contains(path, "/task/t1/comment") {
			_, _ = fmt.Fprintf(w, `{"comments":[{"id":"c1","task_id":"t1","user":{"id":1,"username":"bob"},"comment_text":"hey @ivy-agent can you help?","date":"%d"},{"id":"c2","task_id":"t1","user":{"id":2,"username":"alice"},"comment_text":"old comment","date":"1699900000000"}]}`,
				futureMs)
			return
		}

		_, _ = fmt.Fprintf(w, `{"tasks":[],"last_page":true}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	handler := func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {
		if reason == ReasonMentioned {
			capturedTask = &task
			capturedReason = reason
			capturedMention = mention
		}
	}

	cfg := config.ClickUpConfig{
		TeamID:        "team123",
		PollInterval:  50 * time.Millisecond,
		AgentUsername: "ivy-agent",
	}

	poller := NewPoller(client, cfg, handler, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	poller.Start(ctx)
	time.Sleep(200 * time.Millisecond)

	if capturedReason != ReasonMentioned {
		t.Errorf("expected reason Mentioned, got %s", capturedReason)
	}
	if capturedTask == nil || capturedTask.ID != "t1" {
		t.Error("expected task t1 to be captured")
	}
	if capturedMention == nil {
		t.Fatal("expected mention info")
	}
	if capturedMention.Author != "bob" {
		t.Errorf("expected author bob, got %s", capturedMention.Author)
	}
	if capturedMention.CommentID != "c1" {
		t.Errorf("expected comment c1, got %s", capturedMention.CommentID)
	}
}

func TestPoller_IgnoresOldMentions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if contains(path, "/team/team123/task") {
			_, _ = fmt.Fprintf(w, `{"tasks":[{"id":"t1","name":"Old Mention","status":{"status":"open"},"date_created":"1700000000000","date_updated":"1700001000000"}],"last_page":true}`)
			return
		}

		// Comment is older than the poller start time
		if contains(path, "/task/t1/comment") {
			_, _ = fmt.Fprintf(w, `{"comments":[{"id":"c1","task_id":"t1","user":{"id":1,"username":"bob"},"comment_text":"@ivy-agent help","date":"1000000000000"}]}`)
			return
		}

		_, _ = fmt.Fprintf(w, `{"tasks":[],"last_page":true}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	mentionCalled := false
	handler := func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {
		if reason == ReasonMentioned {
			mentionCalled = true
		}
	}

	cfg := config.ClickUpConfig{
		TeamID:        "team123",
		PollInterval:  50 * time.Millisecond,
		AgentUsername: "ivy-agent",
	}

	poller := NewPoller(client, cfg, handler, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	poller.Start(ctx)
	time.Sleep(200 * time.Millisecond)

	if mentionCalled {
		t.Error("should not have triggered mention handler for old comment")
	}
}

func TestPoller_AssignedReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tasks":[{"id":"t1","name":"Assigned Task","status":{"status":"open"},"date_created":"1700000000000","date_updated":"1700001000000","assignees":[{"id":42,"username":"ivy-agent"}]}],"last_page":true}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	var capturedReason TriggerReason
	handler := func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {
		capturedReason = reason
	}

	cfg := config.ClickUpConfig{
		TeamID:        "team123",
		PollInterval:  50 * time.Millisecond,
		Assignee:      "42",
		AgentUsername: "ivy-agent",
	}

	poller := NewPoller(client, cfg, handler, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	poller.Start(ctx)
	time.Sleep(200 * time.Millisecond)

	if capturedReason != ReasonAssigned {
		t.Errorf("expected reason Assigned, got %s", capturedReason)
	}
}

func TestPoller_StartStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tasks":[],"last_page":true}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	cfg := config.ClickUpConfig{
		TeamID:       "team123",
		PollInterval: 50 * time.Millisecond,
	}

	poller := NewPoller(client, cfg, func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {}, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	poller.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		poller.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() blocked for too long")
	}
}

func TestPoller_DefaultInterval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tasks":[],"last_page":true}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	cfg := config.ClickUpConfig{
		TeamID:       "team123",
		PollInterval: 0,
	}

	poller := NewPoller(client, cfg, func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {}, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	lu := poller.LastUpdated()
	if lu == 0 {
		t.Error("expected LastUpdated to be initialized")
	}

	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestPoller_EmptyResponse(t *testing.T) {
	pollCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollCount.Add(1)
		_, _ = fmt.Fprintf(w, `{"tasks":[],"last_page":true}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	handlerCalled := false
	handler := func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {
		handlerCalled = true
	}

	cfg := config.ClickUpConfig{
		TeamID:       "team123",
		PollInterval: 50 * time.Millisecond,
	}

	poller := NewPoller(client, cfg, handler, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	poller.Start(ctx)
	time.Sleep(150 * time.Millisecond)

	if handlerCalled {
		t.Error("handler should not be called for empty response")
	}
}

func TestPoller_ApiError(t *testing.T) {
	pollCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollCount.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"err_code":"bad_request","message":"Bad request"}`)
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		token:      "pk_test",
		teamID:     "team123",
		logger:     slog.Default(),
		baseURL:    server.URL + "/api/v2",
	}

	handlerCalled := false
	handler := func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo) {
		handlerCalled = true
	}

	cfg := config.ClickUpConfig{
		TeamID:       "team123",
		PollInterval: 50 * time.Millisecond,
	}

	poller := NewPoller(client, cfg, handler, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller.Start(ctx)
	time.Sleep(1 * time.Second)

	if pollCount.Load() < 2 {
		t.Errorf("expected multiple polls, got %d", pollCount.Load())
	}
	if handlerCalled {
		t.Error("handler should not be called on API error")
	}
}

func TestContainsMention(t *testing.T) {
	tests := []struct {
		text    string
		pattern string
		want    bool
	}{
		{"hey @ivy-agent can you help?", "@ivy-agent", true},
		{"@Ivy-Agent help", "@ivy-agent", true},
		{"@IVY-AGENT", "@ivy-agent", true},
		{"no mention here", "@ivy-agent", false},
		{"@ivy something else", "@ivy-agent", false},
		{"cc @ivy-agent", "@ivy-agent", true},
	}

	for _, tt := range tests {
		got := containsMention(tt.text, tt.pattern)
		if got != tt.want {
			t.Errorf("containsMention(%q, %q) = %v, want %v", tt.text, tt.pattern, got, tt.want)
		}
	}
}

// contains is a simple string contains helper for test path matching.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
