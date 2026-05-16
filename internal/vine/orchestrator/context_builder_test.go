package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/aspectrr/ivy/internal/vine/model"
)

func TestContextBuilderSystemPrompt(t *testing.T) {
	cb := NewContextBuilder()
	messages := cb.Build(nil)

	if len(messages) != 1 {
		t.Fatalf("expected 1 message (system), got %d", len(messages))
	}
	if messages[0].Role != "system" {
		t.Fatalf("expected role=system, got %s", messages[0].Role)
	}
	if messages[0].Content != SystemPrompt {
		t.Fatal("system prompt mismatch")
	}
}

func TestContextBuilderWithSkills(t *testing.T) {
	cb := NewContextBuilder()
	cb.SetSkills([]string{"Skill 1: Do things", "Skill 2: Do more things"})

	messages := cb.Build(nil)
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	if !contains(messages[0].Content, "Skill 1: Do things") {
		t.Fatal("expected system prompt to contain skill 1")
	}
	if !contains(messages[0].Content, "Relevant Skills") {
		t.Fatal("expected system prompt to contain skills section header")
	}
}

func TestContextBuilderUserMessage(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeUserMessage,
			Data: mustRawJSON(model.UserMessagePayload{Content: "Hello agent"}),
		},
	}

	messages := cb.Build(events)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(messages))
	}
	if messages[1].Role != "user" {
		t.Fatalf("expected role=user, got %s", messages[1].Role)
	}
	if messages[1].Content != "Hello agent" {
		t.Fatalf("expected content='Hello agent', got %s", messages[1].Content)
	}
}

func TestContextBuilderUserMessageWithAttachments(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeUserMessage,
			Data: mustRawJSON(model.UserMessagePayload{
				Content:     "See attached",
				Attachments: []string{"file1.txt", "file2.log"},
			}),
		},
	}

	messages := cb.Build(events)
	if !contains(messages[1].Content, "Attachments: file1.txt, file2.log") {
		t.Fatalf("expected attachments in content, got: %s", messages[1].Content)
	}
}

func TestContextBuilderAgentMessage(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeAgentMessage,
			Data: mustRawJSON(model.AgentMessagePayload{Content: "I'll help with that"}),
		},
	}

	messages := cb.Build(events)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[1].Role != "assistant" {
		t.Fatalf("expected role=assistant, got %s", messages[1].Role)
	}
}

func TestContextBuilderToolCallAndResult(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeToolCall,
			Data: mustRawJSON(model.ToolCallPayload{
				ToolName: "grep",
				Args:     json.RawMessage(`{"pattern":"error"}`),
				CallID:   "call_abc",
			}),
		},
		{
			Seq:  2,
			Type: model.EventTypeToolResult,
			Data: mustRawJSON(model.ToolResultPayload{
				CallID:  "call_abc",
				Output:  "line 1\nline 2",
				IsError: false,
			}),
		},
	}

	messages := cb.Build(events)
	// system + assistant (tool_call) + tool (result)
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}

	// Assistant message with tool call.
	if messages[1].Role != "assistant" {
		t.Fatalf("expected role=assistant, got %s", messages[1].Role)
	}
	if len(messages[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(messages[1].ToolCalls))
	}
	if messages[1].ToolCalls[0].Function.Name != "grep" {
		t.Fatalf("expected tool name=grep, got %s", messages[1].ToolCalls[0].Function.Name)
	}
	if messages[1].ToolCalls[0].ID != "call_abc" {
		t.Fatalf("expected call_id=call_abc, got %s", messages[1].ToolCalls[0].ID)
	}

	// Tool result message.
	if messages[2].Role != "tool" {
		t.Fatalf("expected role=tool, got %s", messages[2].Role)
	}
	if messages[2].ToolCallID != "call_abc" {
		t.Fatalf("expected tool_call_id=call_abc, got %s", messages[2].ToolCallID)
	}
}

func TestContextBuilderToolResultError(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeToolCall,
			Data: mustRawJSON(model.ToolCallPayload{
				ToolName: "bad_tool",
				Args:     json.RawMessage(`{}`),
				CallID:   "call_err",
			}),
		},
		{
			Seq:  2,
			Type: model.EventTypeToolResult,
			Data: mustRawJSON(model.ToolResultPayload{
				CallID:  "call_err",
				Output:  "command not found",
				IsError: true,
			}),
		},
	}

	messages := cb.Build(events)
	if !contains(messages[2].Content, "Error: command not found") {
		t.Fatalf("expected error prefix in tool result, got: %s", messages[2].Content)
	}
}

func TestContextBuilderStatusTransitionIgnored(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeUserMessage,
			Data: mustRawJSON(model.UserMessagePayload{Content: "hello"}),
		},
		{
			Seq:  2,
			Type: model.EventTypeStatusTransition,
			Data: mustRawJSON(model.StatusTransitionPayload{From: "pending", To: "running"}),
		},
	}

	messages := cb.Build(events)
	// Status transitions should not produce messages.
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(messages))
	}
}

func TestContextBuilderErrorEvent(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeError,
			Data: mustRawJSON(model.ErrorPayload{Message: "LLM rate limited"}),
		},
	}

	messages := cb.Build(events)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[1].Role != "system" {
		t.Fatalf("expected role=system for error event, got %s", messages[1].Role)
	}
	if !contains(messages[1].Content, "LLM rate limited") {
		t.Fatalf("expected error message in content, got: %s", messages[1].Content)
	}
}

func TestContextBuilderInterruptEvent(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeInterrupt,
			Data: mustRawJSON(model.InterruptPayload{
				Reason:         "needs user input",
				RequiresAction: true,
			}),
		},
	}

	messages := cb.Build(events)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[1].Role != "system" {
		t.Fatalf("expected role=system for interrupt, got %s", messages[1].Role)
	}
	if !contains(messages[1].Content, "needs user input") {
		t.Fatalf("expected interrupt reason in content, got: %s", messages[1].Content)
	}
}

func TestContextBuilderTruncation(t *testing.T) {
	cb := NewContextBuilder()
	cb.maxTokens = 200 // Small limit for testing.

	// Create many events that should overflow.
	var events []model.Event
	for i := 0; i < 50; i++ {
		events = append(events, model.Event{
			Seq:  int64(i + 1),
			Type: model.EventTypeUserMessage,
			Data: mustRawJSON(model.UserMessagePayload{
				Content: "This is a longer message that will consume tokens when the context builder processes it.",
			}),
		})
	}

	messages := cb.Build(events)

	// Should still have the system prompt.
	if len(messages) < 2 {
		t.Fatalf("expected at least 2 messages (system + some events), got %d", len(messages))
	}
	if messages[0].Role != "system" {
		t.Fatal("first message must be system prompt")
	}

	// Token estimate should be within budget.
	tokens := cb.estimateTokens(messages)
	if tokens > cb.maxTokens+100 { // small buffer for estimation error
		t.Fatalf("estimated tokens %d exceeds budget %d", tokens, cb.maxTokens)
	}
}

func TestContextBuilderCustomSystemPrompt(t *testing.T) {
	cb := NewContextBuilder()
	cb.SetSystemPrompt("Custom system prompt")

	messages := cb.Build(nil)
	if messages[0].Content != "Custom system prompt" {
		t.Fatalf("expected custom prompt, got: %s", messages[0].Content)
	}
}

func TestContextBuilderFullConversation(t *testing.T) {
	cb := NewContextBuilder()
	events := []model.Event{
		{
			Seq:  1,
			Type: model.EventTypeUserMessage,
			Data: mustRawJSON(model.UserMessagePayload{Content: "Debug my logstash pipeline"}),
		},
		{
			Seq:  2,
			Type: model.EventTypeAgentMessage,
			Data: mustRawJSON(model.AgentMessagePayload{Content: "I'll help. Let me check the config."}),
		},
		{
			Seq:  3,
			Type: model.EventTypeToolCall,
			Data: mustRawJSON(model.ToolCallPayload{
				ToolName: "parser_cat",
				Args:     json.RawMessage(`{"path":"/etc/logstash/logstash.yml"}`),
				CallID:   "call_1",
			}),
		},
		{
			Seq:  4,
			Type: model.EventTypeToolResult,
			Data: mustRawJSON(model.ToolResultPayload{
				CallID: "call_1",
				Output: "http.host: 0.0.0.0\npath.config: /etc/logstash/pipelines",
			}),
		},
		{
			Seq:  5,
			Type: model.EventTypeAgentMessage,
			Data: mustRawJSON(model.AgentMessagePayload{Content: "I found an issue with your pipeline config."}),
		},
	}

	messages := cb.Build(events)

	// system + user + assistant + assistant(tool_call) + tool(result) + assistant
	if len(messages) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(messages))
	}

	expected := []string{"system", "user", "assistant", "assistant", "tool", "assistant"}
	for i, msg := range messages {
		if msg.Role != expected[i] {
			t.Fatalf("message[%d]: expected role=%s, got %s", i, expected[i], msg.Role)
		}
	}
}

func TestMustJSON(t *testing.T) {
	result := mustJSON(map[string]string{"key": "value"})
	if string(result) != `{"key":"value"}` {
		t.Fatalf("unexpected JSON: %s", string(result))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func mustRawJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
