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
		// Return two tasks on first poll, empty after that
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

	handler := func(task Task, isNew bool) {
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

	// Wait for at least one poll
	time.Sleep(200 * time.Millisecond)

	// Should have called handler for both tasks
	if got := handlerCalls.Load(); got != 2 {
		t.Errorf("handler called %d times, want 2", got)
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

	poller := NewPoller(client, cfg, func(task Task, isNew bool) {}, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	poller.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	cancel()

	// Stop should complete after cancel
	done := make(chan struct{})
	go func() {
		poller.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good, Stop returned
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
		PollInterval: 0, // Should default to 30s
	}

	poller := NewPoller(client, cfg, func(task Task, isNew bool) {}, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Initial lastUpdated should be set
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
	handler := func(task Task, isNew bool) {
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
	handler := func(task Task, isNew bool) {
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

	// Should have polled multiple times despite errors
	if pollCount.Load() < 2 {
		t.Errorf("expected multiple polls, got %d", pollCount.Load())
	}
	if handlerCalled {
		t.Error("handler should not be called on API error")
	}
}
