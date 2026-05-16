package model

import (
	"encoding/json"
	"time"
)

// Session status constants.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSuspended = "suspended"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// Event type constants.
const (
	EventTypeUserMessage      = "user_message"
	EventTypeAgentMessage     = "agent_message"
	EventTypeToolCall         = "tool_call"
	EventTypeToolResult       = "tool_result"
	EventTypeInterrupt        = "interrupt"
	EventTypeStatusTransition = "status_transition"
	EventTypeError            = "error"
)

// Session represents a durable agent session.
type Session struct {
	ID          string          `json:"id"`
	Source      string          `json:"source"`
	SourceID    string          `json:"source_id"`
	Status      string          `json:"status"`
	AgentConfig json.RawMessage `json:"agent_config"`
	SandboxID   *string         `json:"sandbox_id"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// Event represents a single entry in the append-only event log.
type Event struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// --- JSONB payload types for each event type ---

// UserMessagePayload is the data for a user_message event.
type UserMessagePayload struct {
	Content     string   `json:"content"`
	Attachments []string `json:"attachments,omitempty"`
}

// AgentMessagePayload is the data for an agent_message event.
type AgentMessagePayload struct {
	Content    string `json:"content"`
	Model      string `json:"model,omitempty"`
	TokensUsed int    `json:"tokens_used,omitempty"`
}

// ToolCallPayload is the data for a tool_call event.
type ToolCallPayload struct {
	ToolName string          `json:"tool_name"`
	Args     json.RawMessage `json:"args"`
	CallID   string          `json:"call_id"`
}

// ToolResultPayload is the data for a tool_result event.
type ToolResultPayload struct {
	CallID  string `json:"call_id"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
}

// InterruptPayload is the data for an interrupt event.
type InterruptPayload struct {
	Reason         string `json:"reason"`
	RequiresAction bool   `json:"requires_action"`
}

// StatusTransitionPayload is the data for a status_transition event.
type StatusTransitionPayload struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ErrorPayload is the data for an error event.
type ErrorPayload struct {
	Message     string `json:"message"`
	StackTrace  string `json:"stack_trace,omitempty"`
	Recoverable bool   `json:"recoverable"`
}
