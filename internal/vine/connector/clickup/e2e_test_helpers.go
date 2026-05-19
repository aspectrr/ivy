package clickup

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aspectrr/ivy/internal/vine/model"
	"github.com/aspectrr/ivy/internal/vine/orchestrator"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// In-memory fakes for session and event stores
// (avoids requiring PostgreSQL for e2e tests)
// ---------------------------------------------------------------------------

// memSessionStore is an in-memory session.Store replacement.
type memSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*model.Session
}

func newMemSessionStore() *memSessionStore {
	return &memSessionStore{sessions: make(map[string]*model.Session)}
}

func (m *memSessionStore) Create(_ context.Context, source, sourceID string, agentConfig json.RawMessage) (*model.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess := &model.Session{
		ID:          uuid.NewString(),
		Source:      source,
		SourceID:    sourceID,
		Status:      model.StatusPending,
		AgentConfig: agentConfig,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	m.sessions[sess.ID] = sess
	return sess, nil
}

func (m *memSessionStore) Get(_ context.Context, id string) (*model.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s: not found", id)
	}
	return s, nil
}

func (m *memSessionStore) GetBySource(_ context.Context, source, sourceID string) (*model.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.Source == source && s.SourceID == sourceID {
			return s, nil
		}
	}
	return nil, fmt.Errorf("session %s/%s: not found", source, sourceID)
}

func (m *memSessionStore) UpdateStatus(_ context.Context, id string, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("session %s: not found", id)
	}
	s.Status = status
	s.UpdatedAt = time.Now()
	return nil
}

func (m *memSessionStore) ClearSource(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("session %s: not found", id)
	}
	s.SourceID = s.SourceID + "::archived::" + s.ID
	s.UpdatedAt = time.Now()
	return nil
}

// memEventStore is an in-memory eventstore.Store replacement.
type memEventStore struct {
	mu     sync.Mutex
	events map[string][]model.Event // sessionID → events
	nextID int64
}

func newMemEventStore() *memEventStore {
	return &memEventStore{events: make(map[string][]model.Event), nextID: 1}
}

func (m *memEventStore) Append(_ context.Context, sessionID string, eventType string, data json.RawMessage) (*model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	seq := int64(len(m.events[sessionID])) + 1
	evt := model.Event{
		ID:        m.nextID,
		SessionID: sessionID,
		Seq:       seq,
		Type:      eventType,
		Data:      data,
		CreatedAt: time.Now(),
	}
	m.nextID++
	m.events[sessionID] = append(m.events[sessionID], evt)
	return &evt, nil
}

func (m *memEventStore) GetEvents(_ context.Context, sessionID string, afterSeq int64, limit int) ([]model.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []model.Event
	for _, evt := range m.events[sessionID] {
		if evt.Seq > afterSeq {
			result = append(result, evt)
		}
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Adapter wrappers to make mem stores compatible with orchestrator
// ---------------------------------------------------------------------------

// orchestratorSessionStore wraps memSessionStore to match the interface
// expected by orchestrator.New(). Since orchestrator.New takes *session.Store,
// we need to use the adapter pattern through the ToolExecutor and inject
// our custom orchestrator logic.
//
// Instead of fighting concrete types, we'll create a testHarness that
// directly drives the orchestrator's runLoop with a fake LLM.

// ---------------------------------------------------------------------------
// Fake LLM client
// ---------------------------------------------------------------------------

// scriptedResponse is a single LLM response in a scripted sequence.
type scriptedResponse struct {
	Content      string
	ToolCalls    []orchestrator.ToolCall
	FinishReason string
}

// fakeLLMClient returns a scripted sequence of responses, then a final answer.
type fakeLLMClient struct {
	responses []scriptedResponse
	calls     int
	mu        sync.Mutex
}

func (f *fakeLLMClient) Chat(_ context.Context, req orchestrator.ChatRequest) (*orchestrator.ChatResponse, error) {
	f.mu.Lock()
	idx := f.calls
	f.calls++
	f.mu.Unlock()

	if idx >= len(f.responses) {
		return nil, fmt.Errorf("fake LLM: unexpected call %d (have %d responses)", idx, len(f.responses))
	}

	resp := f.responses[idx]

	// Resolve dynamic placeholders in tool call arguments.
	// Extract the parent comment ID from the conversation context.
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			if start := strings.Index(msg.Content, "Mention Comment ID: "); start != -1 {
				rest := msg.Content[start+len("Mention Comment ID: "):]
				if space := strings.IndexByte(rest, ' '); space != -1 {
					rest = rest[:space]
				}
				for i := range resp.ToolCalls {
					resp.ToolCalls[i].Function.Arguments = strings.ReplaceAll(
						resp.ToolCalls[i].Function.Arguments,
						"__PARENT_COMMENT_ID__",
						strings.TrimSpace(rest),
					)
				}
			}
			if start := strings.Index(msg.Content, "comment_id="); start != -1 {
				rest := msg.Content[start+len("comment_id="):]
				if space := strings.IndexByte(rest, ' '); space != -1 {
					rest = rest[:space]
				}
				for i := range resp.ToolCalls {
					resp.ToolCalls[i].Function.Arguments = strings.ReplaceAll(
						resp.ToolCalls[i].Function.Arguments,
						"__PARENT_COMMENT_ID__",
						strings.TrimSpace(rest),
					)
				}
			}
		}
	}

	return &orchestrator.ChatResponse{
		ID: fmt.Sprintf("fake-%d", idx),
		Choices: []orchestrator.ChatChoice{
			{
				Index:        0,
				Message:      orchestrator.ChatMessage{Content: resp.Content, ToolCalls: resp.ToolCalls},
				FinishReason: resp.FinishReason,
			},
		},
		Usage: &orchestrator.ChatUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
	}, nil
}

// ---------------------------------------------------------------------------
// Fake tool executor
// ---------------------------------------------------------------------------

// fakeToolExecutor records tool calls and returns canned results.
// For ClickUp tools, it delegates to the real mock ClickUp client.
type fakeToolExecutor struct {
	mu            sync.Mutex
	calls         []toolCallRecord
	clickupClient *Client
}

type toolCallRecord struct {
	Name string
	Args string
}

func (f *fakeToolExecutor) ExecuteTool(ctx context.Context, name string, args json.RawMessage, sessionID string) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, toolCallRecord{Name: name, Args: string(args)})
	f.mu.Unlock()

	// For ClickUp tools, delegate to the real mock ClickUp client
	switch name {
	case "clickup_post_comment":
		var p struct {
			TaskID string `json:"task_id"`
			Text   string `json:"text"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		comment, err := f.clickupClient.PostComment(ctx, p.TaskID, p.Text)
		if err != nil {
			return nil, err
		}
		return json.Marshal(comment)
	case "clickup_reply_comment":
		var p struct {
			CommentID json.Number `json:"comment_id"`
			Text      string      `json:"text"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, err
		}
		comment, err := f.clickupClient.ReplyToComment(ctx, p.CommentID, p.Text)
		if err != nil {
			return nil, err
		}
		return json.Marshal(comment)
	case "list_parser_hosts":
		return json.RawMessage(`[{"host_id":"parser-01","hostname":"log-parser-01.prod"},{"host_id":"parser-02","hostname":"log-parser-02.prod"}]`), nil
	case "exec_on_host":
		return json.RawMessage(`{"stdout":"2024-01-15T10:23:45Z [INFO] Processing logs from kafka-consumer-group-1\n2024-01-15T10:23:46Z [WARN] grok parse failure for message: {\\"message\\\":\\"GET /api/health 200 3ms\\"}\n2024-01-15T10:23:47Z [INFO] 2.3k events/s throughput","stderr":"","exit_code":0}`), nil
	case "create_sandbox":
		return json.RawMessage(`{"session_id":"` + sessionID + `","logstash_version":"8.12.0","config_files":["01-input.conf","02-filter.conf","03-output.conf"],"status":"ready"}`), nil
	case "pipeline_health":
		return json.RawMessage(`{"redpanda":"healthy","logstash":"healthy","elasticsearch":"healthy","messages_processed":1500,"errors":0}`), nil
	default:
		return json.RawMessage(`{"result":"ok"}`), nil
	}
}

func (f *fakeToolExecutor) getCalls() []toolCallRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolCallRecord{}, f.calls...)
}

// ---------------------------------------------------------------------------
// testOrchestrator wraps the orchestrator with in-memory stores
// ---------------------------------------------------------------------------

// We can't directly use orchestrator.New because it takes *session.Store
// and *eventstore.Store (concrete types). Instead, we'll build a mini
// orchestrator that mimics the runLoop but uses our in-memory stores.

type testOrchestrator struct {
	sessions   *memSessionStore
	events     *memEventStore
	llm        *fakeLLMClient
	toolExec   *fakeToolExecutor
	ctxBuilder *orchestrator.ContextBuilder
	toolDefs   []orchestrator.ToolDef
	logger     *slog.Logger
}

func newTestOrchestrator(sessions *memSessionStore, events *memEventStore, llm *fakeLLMClient, tools *fakeToolExecutor, logger *slog.Logger) *testOrchestrator {
	cb := orchestrator.NewContextBuilder()
	return &testOrchestrator{
		sessions:   sessions,
		events:     events,
		llm:        llm,
		toolExec:   tools,
		ctxBuilder: cb,
		logger:     logger,
	}
}

func (t *testOrchestrator) SetTools(tools []orchestrator.ToolDef) {
	t.ctxBuilder.SetTools(tools)
}

// StartRun begins the agent loop (mirrors orchestrator.StartRun).
func (t *testOrchestrator) StartRun(ctx context.Context, sessionID string) error {
	sess, err := t.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.Status != model.StatusPending && sess.Status != model.StatusSuspended {
		return fmt.Errorf("cannot start run for session in status %s", sess.Status)
	}
	if err := t.sessions.UpdateStatus(ctx, sessionID, model.StatusRunning); err != nil {
		return err
	}
	go t.runLoop(ctx, sessionID)
	return nil
}

func (t *testOrchestrator) Resume(ctx context.Context, sessionID string) error {
	return t.StartRun(ctx, sessionID)
}

func (t *testOrchestrator) Terminate(ctx context.Context, sessionID string, failed bool) error {
	status := model.StatusCompleted
	if failed {
		status = model.StatusFailed
	}
	return t.sessions.UpdateStatus(ctx, sessionID, status)
}

// runLoop mirrors orchestrator.runLoop.
func (t *testOrchestrator) runLoop(ctx context.Context, sessionID string) {
	const maxIterations = 30
	for i := 0; i < maxIterations; i++ {
		sess, err := t.sessions.Get(ctx, sessionID)
		if err != nil || sess.Status != model.StatusRunning {
			return
		}

		events, err := t.events.GetEvents(ctx, sessionID, 0, 10000)
		if err != nil {
			t.Terminate(ctx, sessionID, true)
			return
		}

		messages := t.ctxBuilder.Build(events)

		resp, err := t.llm.Chat(ctx, orchestrator.ChatRequest{
			Model:    "test-model",
			Messages: messages,
			Tools:    t.toolDefs,
		})
		if err != nil {
			t.logger.Error("fake LLM error", "error", err)
			t.Terminate(ctx, sessionID, true)
			return
		}

		if len(resp.Choices) == 0 {
			t.Terminate(ctx, sessionID, true)
			return
		}

		choice := resp.Choices[0]

		// Handle tool calls
		if len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				t.events.Append(ctx, sessionID, model.EventTypeToolCall, mustJSONE2E(model.ToolCallPayload{
					ToolName: tc.Function.Name,
					Args:     json.RawMessage(tc.Function.Arguments),
					CallID:   tc.ID,
				}))

				result, toolErr := t.toolExec.ExecuteTool(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments), sessionID)

				output := string(result)
				isError := false
				if toolErr != nil {
					output = toolErr.Error()
					isError = true
				}

				t.events.Append(ctx, sessionID, model.EventTypeToolResult, mustJSONE2E(model.ToolResultPayload{
					CallID:  tc.ID,
					Output:  output,
					IsError: isError,
				}))
			}
			continue
		}

		// Handle agent message
		if choice.Message.Content != "" {
			t.events.Append(ctx, sessionID, model.EventTypeAgentMessage, mustJSONE2E(model.AgentMessagePayload{
				Content: choice.Message.Content,
			}))

			if choice.FinishReason == "stop" {
				t.Terminate(ctx, sessionID, false)
				return
			}
		}

		t.Terminate(ctx, sessionID, false)
		return
	}
}

// ---------------------------------------------------------------------------
// Helper to get tools from the context builder
// ---------------------------------------------------------------------------

// We need to expose the tools from ContextBuilder. Let me add a method.

// Actually, let me just get the tools by reading the unexported field via
// a public accessor. Since ContextBuilder is in another package, we'll
// just construct the tool defs we need directly.

func mustJSONE2E(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

// waitForStatus polls until the session reaches the expected status or times out.
func waitForStatus(ctx context.Context, store *memSessionStore, sessionID, expected string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sess, err := store.Get(ctx, sessionID)
		if err != nil {
			return err
		}
		if sess.Status == expected {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	sess, _ := store.Get(ctx, sessionID)
	return fmt.Errorf("timed out waiting for status %s (got %s)", expected, sess.Status)
}
