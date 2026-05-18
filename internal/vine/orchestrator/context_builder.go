package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aspectrr/ivy/internal/vine/model"
)

const (
	// MaxContextTokens is a rough upper bound on context window.
	// The builder will truncate older events if approaching this limit.
	MaxContextTokens = 128000

	// Rough estimate: 1 token ≈ 4 characters.
	charsPerToken = 4

	// SystemPrompt is the base system prompt injected into every session.
	SystemPrompt = `You are Ivy, a data engineering agent specializing in log pipeline operations.
You work with Logstash, Kafka, and Elasticsearch to debug, configure, and optimize log parsing pipelines.

Available capabilities:
- Read and write files in your workspace sandbox
- Execute bash commands in your workspace sandbox
- Run read-only diagnostic commands on log parser hosts (grep, awk, find, cat, tail, systemctl status, journalctl)
- Create and test pipeline configurations (Kafka → Logstash → Elasticsearch)
- Search past session history for relevant context
- Search and create skills for future reference

Guidelines:
- Always test pipeline changes before reporting completion
- Search history when encountering unfamiliar issues
- Create a skill at the end of each session documenting what you learned
- Be thorough but concise in your explanations`
)

// ContextBuilder constructs the message array for the LLM from session events.
type ContextBuilder struct {
	systemPrompt string
	tools        []ToolDef
	skills       []string
	maxTokens    int
}

// NewContextBuilder creates a new context builder.
func NewContextBuilder() *ContextBuilder {
	return &ContextBuilder{
		systemPrompt: SystemPrompt,
		maxTokens:    MaxContextTokens,
	}
}

// SetSystemPrompt overrides the base system prompt.
func (cb *ContextBuilder) SetSystemPrompt(prompt string) {
	cb.systemPrompt = prompt
}

// SetTools sets the available tool definitions.
func (cb *ContextBuilder) SetTools(tools []ToolDef) {
	cb.tools = tools
}

// SetSkills injects skill content into the system prompt.
func (cb *ContextBuilder) SetSkills(skills []string) {
	cb.skills = skills
}

// MaxTokens returns the configured max token limit.
func (cb *ContextBuilder) MaxTokens() int {
	return cb.maxTokens
}

// Build constructs the chat messages from session events.
// If a compacted event exists, it uses the summary as context and only converts
// events after the compaction point. Otherwise, converts all events and truncates
// if the context window is exceeded.
func (cb *ContextBuilder) Build(events []model.Event) []ChatMessage {
	var messages []ChatMessage

	// Build system message with skills injected.
	systemContent := cb.systemPrompt
	if len(cb.skills) > 0 {
		systemContent += "\n\n## Relevant Skills\n\n" + strings.Join(cb.skills, "\n\n---\n\n")
	}

	// Find the most recent compacted event.
	var latestCompacted *model.CompactedPayload
	var startIdx int
	for i, evt := range events {
		if evt.Type == model.EventTypeCompacted {
			var p model.CompactedPayload
			if err := json.Unmarshal(evt.Data, &p); err == nil {
				latestCompacted = &p
				startIdx = i + 1
			}
		}
	}

	// If we have a compacted summary, inject it after the system prompt.
	if latestCompacted != nil {
		systemContent += "\n\n## Previous Conversation Summary\n\n" + latestCompacted.Summary
	}

	messages = append(messages, ChatMessage{Role: "system", Content: systemContent})

	// Convert events after the compaction point.
	for _, evt := range events[startIdx:] {
		msgs := cb.eventToMessages(evt)
		messages = append(messages, msgs...)
	}

	// Truncate from the front (keep system + recent events) if over budget.
	messages = cb.truncate(messages)

	return messages
}

// eventToMessages converts a single event to one or more chat messages.
func (cb *ContextBuilder) eventToMessages(evt model.Event) []ChatMessage {
	switch evt.Type {
	case model.EventTypeUserMessage:
		var p model.UserMessagePayload
		if err := json.Unmarshal(evt.Data, &p); err != nil {
			return nil
		}
		content := p.Content
		if len(p.Attachments) > 0 {
			content += "\n\nAttachments: " + strings.Join(p.Attachments, ", ")
		}
		return []ChatMessage{{Role: "user", Content: content}}

	case model.EventTypeAgentMessage:
		var p model.AgentMessagePayload
		if err := json.Unmarshal(evt.Data, &p); err != nil {
			return nil
		}
		return []ChatMessage{{Role: "assistant", Content: p.Content}}

	case model.EventTypeToolCall:
		var p model.ToolCallPayload
		if err := json.Unmarshal(evt.Data, &p); err != nil {
			return nil
		}
		return []ChatMessage{
			{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{
						ID:   p.CallID,
						Type: "function",
						Function: FunctionCall{
							Name:      p.ToolName,
							Arguments: string(p.Args),
						},
					},
				},
			},
		}

	case model.EventTypeToolResult:
		var p model.ToolResultPayload
		if err := json.Unmarshal(evt.Data, &p); err != nil {
			return nil
		}
		content := p.Output
		if p.IsError {
			content = "Error: " + content
		}
		return []ChatMessage{
			{
				Role:       "tool",
				Content:    content,
				ToolCallID: p.CallID,
			},
		}

	case model.EventTypeInterrupt:
		var p model.InterruptPayload
		if err := json.Unmarshal(evt.Data, &p); err != nil {
			return nil
		}
		return []ChatMessage{
			{
				Role:    "system",
				Content: fmt.Sprintf("[Interrupt: %s]", p.Reason),
			},
		}

	case model.EventTypeStatusTransition:
		// Status transitions are not sent to the LLM.
		return nil

	case model.EventTypeError:
		var p model.ErrorPayload
		if err := json.Unmarshal(evt.Data, &p); err != nil {
			return nil
		}
		return []ChatMessage{
			{
				Role:    "system",
				Content: fmt.Sprintf("[Error: %s]", p.Message),
			},
		}

	case model.EventTypeCompacted:
		// Compacted events are handled by Build() directly (injected into system prompt).
		// They don't produce individual messages.
		return nil

	default:
		return nil
	}
}

// truncate removes oldest messages (keeping system prompt) if over budget.
func (cb *ContextBuilder) truncate(messages []ChatMessage) []ChatMessage {
	if len(messages) <= 1 {
		return messages
	}

	// Always keep the system message at index 0.
	system := messages[0]
	rest := messages[1:]

	for cb.EstimateTokens(append([]ChatMessage{system}, rest...)) > cb.maxTokens && len(rest) > 1 {
		rest = rest[1:]
	}

	return append([]ChatMessage{system}, rest...)
}

// EstimateTokens gives a rough token count for the messages.
func (cb *ContextBuilder) EstimateTokens(messages []ChatMessage) int {
	total := 0
	for _, m := range messages {
		total += len(m.Content) / charsPerToken
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments) / charsPerToken
			total += len(tc.Function.Name) / charsPerToken
		}
	}
	return total
}
