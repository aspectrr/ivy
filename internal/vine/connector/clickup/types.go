package clickup

import "context"

// ToolClient is the interface that ClickUp tools use.
// This decouples the tools from the concrete HTTP client.
type ToolClient interface {
	GetTask(ctx context.Context, taskID string) (*Task, error)
	UpdateTask(ctx context.Context, taskID string, updates map[string]interface{}) (*Task, error)
	PostComment(ctx context.Context, taskID string, text string) (*Comment, error)
	GetComments(ctx context.Context, taskID string) ([]Comment, error)
	GetAttachments(ctx context.Context, taskID string) ([]Attachment, error)
	GetTeamTasks(ctx context.Context, opts *TaskListOpts) ([]Task, error)
}
