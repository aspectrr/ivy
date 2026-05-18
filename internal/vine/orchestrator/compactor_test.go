package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/aspectrr/ivy/internal/vine/model"
)

func TestFindCompactionCutoff(t *testing.T) {
	tests := []struct {
		name        string
		events      []model.Event
		maxTokens   int
		targetRatio float64
		wantZero    bool // true if we expect 0 (no compaction needed)
	}{
		{
			name: "few events, no compaction",
			events: []model.Event{
				{Seq: 1, Type: model.EventTypeUserMessage, Data: mustRawJSON(model.UserMessagePayload{Content: "hello"})},
				{Seq: 2, Type: model.EventTypeAgentMessage, Data: mustRawJSON(model.AgentMessagePayload{Content: "hi"})},
			},
			maxTokens:   128000,
			targetRatio: 0.40,
			wantZero:    true,
		},
		{
			name: "many events, should compact",
			events: func() []model.Event {
				var evts []model.Event
				for i := 0; i < 100; i++ {
					evts = append(evts, model.Event{
						Seq:  int64(i + 1),
						Type: model.EventTypeUserMessage,
						Data: mustRawJSON(model.UserMessagePayload{
							Content: "This is a longer message that takes up tokens in the context window for testing purposes.",
						}),
					})
				}
				return evts
			}(),
			maxTokens:   1000,
			targetRatio: 0.40,
			wantZero:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cutoff := findCompactionCutoff(tt.events, tt.maxTokens, tt.targetRatio)
			if tt.wantZero && cutoff != 0 {
				t.Errorf("expected cutoff=0 (no compaction), got %d", cutoff)
			}
			if !tt.wantZero && cutoff == 0 {
				t.Error("expected non-zero cutoff (compaction needed), got 0")
			}
			// Verify the cutoff is at a clean boundary.
			if cutoff > 0 && cutoff < len(tt.events) {
				if !isCleanBoundary(tt.events, cutoff) {
					t.Errorf("cutoff at %d is not a clean boundary", cutoff)
				}
			}
		})
	}
}

func TestIsCleanBoundary(t *testing.T) {
	tests := []struct {
		name  string
		prev  string
		curr  string
		clean bool
	}{
		{"user then agent", model.EventTypeUserMessage, model.EventTypeAgentMessage, true},
		{"agent then user", model.EventTypeAgentMessage, model.EventTypeUserMessage, true},
		{"tool_call then tool_result", model.EventTypeToolCall, model.EventTypeToolResult, false},
		{"tool_result then agent", model.EventTypeToolResult, model.EventTypeAgentMessage, true},
		{"user then tool_result", model.EventTypeUserMessage, model.EventTypeToolResult, false},
		{"agent then tool_call", model.EventTypeAgentMessage, model.EventTypeToolCall, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []model.Event{
				{Seq: 1, Type: tt.prev, Data: json.RawMessage(`{}`)},
				{Seq: 2, Type: tt.curr, Data: json.RawMessage(`{}`)},
			}
			got := isCleanBoundary(events, 1)
			if got != tt.clean {
				t.Errorf("isCleanBoundary(%s, %s) = %v, want %v", tt.prev, tt.curr, got, tt.clean)
			}
		})
	}
}

func TestEstimateEventChars(t *testing.T) {
	evt := model.Event{
		Data: json.RawMessage(`{"content":"hello world"}`),
	}
	got := estimateEventChars(evt)
	if got != len(`{"content":"hello world"}`) {
		t.Errorf("expected %d, got %d", len(`{"content":"hello world"}`), got)
	}
}

func TestNeedsCompaction(t *testing.T) {
	cb := NewContextBuilder()
	cb.maxTokens = 1000 // small limit for testing

	compactor := NewCompactor(nil, nil, cb, nil)

	// Under threshold — should not need compaction.
	smallMessages := []ChatMessage{
		{Role: "system", Content: "short"},
		{Role: "user", Content: "short message"},
	}
	if compactor.NeedsCompaction(smallMessages) {
		t.Error("should not need compaction for small context")
	}

	// Over threshold — should need compaction.
	var bigContent string
	for i := 0; i < 3000; i++ {
		bigContent += "This is a long message that fills up the context window. "
	}
	bigMessages := []ChatMessage{
		{Role: "system", Content: bigContent},
		{Role: "user", Content: bigContent},
	}
	if !compactor.NeedsCompaction(bigMessages) {
		t.Error("should need compaction for large context")
	}
}

func TestContextBuilderWithCompactedEvent(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeUserMessage,
			Data: mustRawJSON(model.UserMessagePayload{Content: "Old message"}),
		},
		{
			Seq:  2,
			Type: model.EventTypeAgentMessage,
			Data: mustRawJSON(model.AgentMessagePayload{Content: "Old response"}),
		},
		{
			Seq:  3,
			Type: model.EventTypeCompacted,
			Data: mustRawJSON(model.CompactedPayload{
				Summary:          "User asked about pipeline, agent checked config.",
				CompactedUpToSeq: 2,
				TokensSaved:      500,
			}),
		},
		{
			Seq:  4,
			Type: model.EventTypeUserMessage,
			Data: mustRawJSON(model.UserMessagePayload{Content: "New message after compaction"}),
		},
	}

	messages := cb.Build(events)

	// System + system (with summary) is combined into one system message + 1 user message.
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages (system with summary + user), got %d", len(messages))
	}

	// System message should contain the compaction summary.
	if messages[0].Role != "system" {
		t.Fatalf("expected system message, got %s", messages[0].Role)
	}
	if !containsStr(messages[0].Content, "Previous Conversation Summary") {
		t.Fatal("expected system prompt to contain conversation summary section")
	}
	if !containsStr(messages[0].Content, "User asked about pipeline") {
		t.Fatal("expected system prompt to contain the summary text")
	}

	// Only the post-compaction user message should appear.
	if messages[1].Role != "user" {
		t.Fatalf("expected user message, got %s", messages[1].Role)
	}
	if messages[1].Content != "New message after compaction" {
		t.Fatalf("expected 'New message after compaction', got %s", messages[1].Content)
	}
}

func TestContextBuilderCompactedEventIgnoredInEventToMessages(t *testing.T) {
	cb := NewContextBuilder()
	evt := model.Event{
		Seq:  1,
		Type: model.EventTypeCompacted,
		Data: mustRawJSON(model.CompactedPayload{Summary: "test"}),
	}
	msgs := cb.eventToMessages(evt)
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages from compacted event, got %d", len(msgs))
	}
}

func TestEventsToText(t *testing.T) {
	cb := NewContextBuilder()
	compactor := NewCompactor(nil, nil, cb, nil)

	events := []model.Event{
		{Seq: 1, Type: model.EventTypeUserMessage, Data: mustRawJSON(model.UserMessagePayload{Content: "Check the pipeline"})},
		{Seq: 2, Type: model.EventTypeAgentMessage, Data: mustRawJSON(model.AgentMessagePayload{Content: "Looking into it"})},
		{Seq: 3, Type: model.EventTypeToolCall, Data: mustRawJSON(model.ToolCallPayload{ToolName: "parser_cat", Args: json.RawMessage(`{"path":"/etc/logstash/conf.d/main.conf"}`), CallID: "c1"})},
		{Seq: 4, Type: model.EventTypeToolResult, Data: mustRawJSON(model.ToolResultPayload{CallID: "c1", Output: "input { kafka { ... } }"})},
		{Seq: 5, Type: model.EventTypeError, Data: mustRawJSON(model.ErrorPayload{Message: "timeout"})},
		{Seq: 6, Type: model.EventTypeInterrupt, Data: mustRawJSON(model.InterruptPayload{Reason: "user stopped"})},
	}

	text := compactor.eventsToText(events)

	if !containsStr(text, "[User]: Check the pipeline") {
		t.Error("expected user message in text")
	}
	if !containsStr(text, "[Agent]: Looking into it") {
		t.Error("expected agent message in text")
	}
	if !containsStr(text, "[Tool Call: parser_cat]") {
		t.Error("expected tool call in text")
	}
	if !containsStr(text, "[Tool Result]: input { kafka") {
		t.Error("expected tool result in text")
	}
	if !containsStr(text, "[Error]: timeout") {
		t.Error("expected error in text")
	}
	if !containsStr(text, "[Interrupt]: user stopped") {
		t.Error("expected interrupt in text")
	}
}

func TestEventsToTextTruncatesLongOutput(t *testing.T) {
	cb := NewContextBuilder()
	compactor := NewCompactor(nil, nil, cb, nil)

	longOutput := ""
	for i := 0; i < 3000; i++ {
		longOutput += "x"
	}

	events := []model.Event{
		{Seq: 1, Type: model.EventTypeToolResult, Data: mustRawJSON(model.ToolResultPayload{CallID: "c1", Output: longOutput})},
	}

	text := compactor.eventsToText(events)

	if containsStr(text, "truncated") == false {
		t.Error("expected long output to be truncated")
	}
}

func TestCompactionPrompt(t *testing.T) {
	// Verify the compaction prompt contains the key requirements.
	if !containsStr(compactionPrompt, "What was accomplished") {
		t.Error("compaction prompt should ask what was accomplished")
	}
	if !containsStr(compactionPrompt, "Current work in progress") {
		t.Error("compaction prompt should ask about work in progress")
	}
	if !containsStr(compactionPrompt, "Files involved") {
		t.Error("compaction prompt should ask about files involved")
	}
	if !containsStr(compactionPrompt, "Next steps") {
		t.Error("compaction prompt should ask about next steps")
	}
	if !containsStr(compactionPrompt, "Key user requests") {
		t.Error("compaction prompt should ask about key requests")
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
