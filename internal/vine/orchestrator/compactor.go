package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aspectrr/ivy/internal/vine/eventstore"
	"github.com/aspectrr/ivy/internal/vine/model"
)

const (
	// CompactionThreshold is the fraction of MaxContextTokens at which compaction triggers.
	CompactionThreshold = 0.85

	// TargetCompactionRatio is the fraction of events to remove during compaction.
	// After compaction, context should be around this fraction of max tokens.
	TargetCompactionRatio = 0.40

	// compactionPrompt is the system prompt used for the compaction LLM call.
	compactionPrompt = `Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions. This summary will be used as context when continuing the conversation, so preserve critical information including:
- What was accomplished
- Current work in progress
- Files involved
- Next steps
- Key user requests or constraints`
)

// Compactor handles automatic context compaction when the context window fills up.
type Compactor struct {
	events  *eventstore.Store
	llm     *LLMClient
	builder *ContextBuilder
	logger  *slog.Logger
}

// NewCompactor creates a new compactor.
func NewCompactor(events *eventstore.Store, llm *LLMClient, builder *ContextBuilder, logger *slog.Logger) *Compactor {
	return &Compactor{
		events:  events,
		llm:     llm,
		builder: builder,
		logger:  logger,
	}
}

// NeedsCompaction returns true if the token estimate exceeds the compaction threshold.
func (c *Compactor) NeedsCompaction(messages []ChatMessage) bool {
	tokens := c.builder.EstimateTokens(messages)
	threshold := int(float64(c.builder.maxTokens) * CompactionThreshold)
	return tokens > threshold
}

// Compact summarizes older events and replaces them with a compacted event.
// Returns the number of tokens saved, or an error.
func (c *Compactor) Compact(ctx context.Context, sessionID string, events []model.Event) (int, error) {
	if len(events) < 4 {
		// Not enough events to meaningfully compact.
		return 0, nil
	}

	// Find a safe cutoff point. We want to compact roughly 60% of the context,
	// leaving the most recent 40% intact. The cutoff must be at a clean boundary
	// (after a tool result, agent message, or user message — never mid-tool-call).
	cutoffIdx := findCompactionCutoff(events, c.builder.maxTokens, TargetCompactionRatio)
	if cutoffIdx < 1 {
		return 0, nil
	}

	// Build a text representation of events to summarize.
	eventsToCompact := events[:cutoffIdx]
	conversationText := c.eventsToText(eventsToCompact)

	if strings.TrimSpace(conversationText) == "" {
		return 0, nil
	}

	c.logger.Info("compacting context",
		"session_id", sessionID,
		"events_compacted", len(eventsToCompact),
		"events_kept", len(events)-cutoffIdx,
	)

	// Call the LLM to summarize.
	summary, err := c.summarize(ctx, conversationText)
	if err != nil {
		return 0, fmt.Errorf("summarizing context: %w", err)
	}

	// Estimate tokens before and after.
	beforeTokens := c.builder.EstimateTokens(c.builder.Build(events))
	afterTokens := len(summary) / charsPerToken
	tokensSaved := beforeTokens - afterTokens
	if tokensSaved < 0 {
		tokensSaved = 0
	}

	// Append the compacted event.
	_, err = c.events.Append(ctx, sessionID, model.EventTypeCompacted, mustJSON(model.CompactedPayload{
		Summary:          summary,
		CompactedUpToSeq: eventsToCompact[len(eventsToCompact)-1].Seq,
		TokensSaved:      tokensSaved,
	}))
	if err != nil {
		return 0, fmt.Errorf("appending compacted event: %w", err)
	}

	c.logger.Info("context compacted",
		"session_id", sessionID,
		"tokens_saved", tokensSaved,
		"summary_length", len(summary),
	)

	return tokensSaved, nil
}

// summarize calls the LLM to produce a conversation summary.
func (c *Compactor) summarize(ctx context.Context, conversationText string) (string, error) {
	resp, err := c.llm.Chat(ctx, ChatRequest{
		Model: "", // use default model
		Messages: []ChatMessage{
			{Role: "system", Content: compactionPrompt},
			{Role: "user", Content: conversationText},
		},
	})
	if err != nil {
		return "", err
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("LLM returned empty summary")
	}

	return resp.Choices[0].Message.Content, nil
}

// eventsToText converts events into a text representation suitable for summarization.
func (c *Compactor) eventsToText(events []model.Event) string {
	var sb strings.Builder

	for _, evt := range events {
		switch evt.Type {
		case model.EventTypeUserMessage:
			var p model.UserMessagePayload
			if err := json.Unmarshal(evt.Data, &p); err == nil {
				fmt.Fprintf(&sb, "[User]: %s\n", p.Content)
			}

		case model.EventTypeAgentMessage:
			var p model.AgentMessagePayload
			if err := json.Unmarshal(evt.Data, &p); err == nil {
				fmt.Fprintf(&sb, "[Agent]: %s\n", p.Content)
			}

		case model.EventTypeToolCall:
			var p model.ToolCallPayload
			if err := json.Unmarshal(evt.Data, &p); err == nil {
				fmt.Fprintf(&sb, "[Tool Call: %s]: %s\n", p.ToolName, string(p.Args))
			}

		case model.EventTypeToolResult:
			var p model.ToolResultPayload
			if err := json.Unmarshal(evt.Data, &p); err == nil {
				prefix := "[Tool Result]"
				if p.IsError {
					prefix = "[Tool Error]"
				}
				// Truncate very long tool outputs to keep the summary prompt reasonable.
				output := p.Output
				if len(output) > 2000 {
					output = output[:2000] + "\n... (truncated)"
				}
				fmt.Fprintf(&sb, "%s: %s\n", prefix, output)
			}

		case model.EventTypeError:
			var p model.ErrorPayload
			if err := json.Unmarshal(evt.Data, &p); err == nil {
				fmt.Fprintf(&sb, "[Error]: %s\n", p.Message)
			}

		case model.EventTypeInterrupt:
			var p model.InterruptPayload
			if err := json.Unmarshal(evt.Data, &p); err == nil {
				fmt.Fprintf(&sb, "[Interrupt]: %s\n", p.Reason)
			}

		case model.EventTypeCompacted:
			var p model.CompactedPayload
			if err := json.Unmarshal(evt.Data, &p); err == nil {
				fmt.Fprintf(&sb, "[Previous Context Summary]: %s\n", p.Summary)
			}

			// Skip status transitions — not relevant for summarization.
		}
	}

	return sb.String()
}

// findCompactionCutoff finds the index at which to split events for compaction.
// Events before the cutoff will be summarized; events after are kept intact.
// The cutoff is placed so that the remaining events consume roughly targetRatio of maxTokens.
// It ensures the cut happens at a clean boundary (not in the middle of a tool call/result pair).
func findCompactionCutoff(events []model.Event, maxTokens int, targetRatio float64) int {
	// Walk from the end backwards, estimating tokens for the tail.
	charBudget := int(float64(maxTokens) * targetRatio * charsPerToken)
	charCount := 0

	for i := len(events) - 1; i >= 0; i-- {
		evt := events[i]
		evtChars := estimateEventChars(evt)
		charCount += evtChars

		if charCount > charBudget {
			// We've gone too far — the cutoff is at i+1.
			// But we need to adjust to a clean boundary.
			cutoff := i + 1

			// Walk forward to find a clean boundary.
			for cutoff < len(events) {
				if isCleanBoundary(events, cutoff) {
					return cutoff
				}
				cutoff++
			}
			return len(events) // can't find a clean cut, don't compact
		}
	}

	// All events fit within the target — no compaction needed.
	return 0
}

// isCleanBoundary checks whether cutting at index i leaves the tail with
// complete tool call/result pairs. A clean boundary is after a user message,
// agent message, tool result, or error.
func isCleanBoundary(events []model.Event, i int) bool {
	if i >= len(events) {
		return true
	}
	prevIdx := i - 1
	if prevIdx < 0 {
		return true
	}

	prev := events[prevIdx]
	curr := events[i]

	// Don't cut between a tool call and its result.
	if prev.Type == model.EventTypeToolCall && curr.Type == model.EventTypeToolResult {
		return false
	}

	// Don't cut if the next event is a tool result without its preceding tool call.
	if curr.Type == model.EventTypeToolResult && prev.Type != model.EventTypeToolCall {
		return false
	}

	return true
}

// estimateEventChars gives a rough character count for an event.
func estimateEventChars(evt model.Event) int {
	// Rough estimate based on data size. The JSON overhead is minimal.
	return len(evt.Data)
}
