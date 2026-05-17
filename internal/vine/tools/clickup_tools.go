package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aspectrr/ivy/internal/vine/connector/clickup"
)

// ClickUpClient is the interface ClickUp tools use.
type ClickUpClient interface {
	GetTask(ctx context.Context, taskID string) (*clickup.Task, error)
	UpdateTask(ctx context.Context, taskID string, updates map[string]interface{}) (*clickup.Task, error)
	PostComment(ctx context.Context, taskID string, text string) (*clickup.Comment, error)
	GetComments(ctx context.Context, taskID string) ([]clickup.Comment, error)
	GetAttachments(ctx context.Context, taskID string) ([]clickup.Attachment, error)
	GetTeamTasks(ctx context.Context, opts *clickup.TaskListOpts) ([]clickup.Task, error)
}

// clickupTool is a generic ClickUp tool with a client reference.
type clickupTool struct {
	name        string
	description string
	parameters  json.RawMessage
	execute     func(ctx context.Context, args json.RawMessage, client ClickUpClient) (json.RawMessage, error)
	client      ClickUpClient
}

func (t *clickupTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.name,
		Description: t.description,
		Parameters:  t.parameters,
	}
}

func (t *clickupTool) Execute(ctx context.Context, args json.RawMessage, _ ToolContext) (json.RawMessage, error) {
	if t.client == nil {
		return nil, fmt.Errorf("clickup tools not available: no client configured")
	}
	return t.execute(ctx, args, t.client)
}

// RegisterClickUpTools registers all ClickUp tools.
func RegisterClickUpTools(registry *Registry, client ClickUpClient) error {
	tools := []clickupTool{
		{
			name:        "clickup_get_task",
			description: "Get full details of a ClickUp task by ID. Returns name, status, description, assignees, tags, dates, and other metadata.",
			parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string","description":"The ClickUp task ID"}},"required":["task_id"]}`),
			client:      client,
			execute: func(ctx context.Context, args json.RawMessage, c ClickUpClient) (json.RawMessage, error) {
				var params struct {
					TaskID string `json:"task_id"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("parsing args: %w", err)
				}
				task, err := c.GetTask(ctx, params.TaskID)
				if err != nil {
					return nil, err
				}
				return json.Marshal(task)
			},
		},
		{
			name:        "clickup_update_task",
			description: "Update a ClickUp task. Can change name, description, status, priority, assignees, and due date.",
			parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"task_id":{"type":"string","description":"The ClickUp task ID"},
					"name":{"type":"string","description":"New task name"},
					"description":{"type":"string","description":"New task description"},
					"status":{"type":"string","description":"New status name (must match a valid status in the list)"},
					"priority":{"type":"integer","description":"Priority: 1=urgent, 2=high, 3=normal, 4=low"},
					"due_date":{"type":"string","description":"Due date as Unix ms timestamp"}
				},
				"required":["task_id"]
			}`),
			client: client,
			execute: func(ctx context.Context, args json.RawMessage, c ClickUpClient) (json.RawMessage, error) {
				var params struct {
					TaskID      string `json:"task_id"`
					Name        string `json:"name"`
					Description string `json:"description"`
					Status      string `json:"status"`
					Priority    *int   `json:"priority"`
					DueDate     string `json:"due_date"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("parsing args: %w", err)
				}

				updates := make(map[string]interface{})
				if params.Name != "" {
					updates["name"] = params.Name
				}
				if params.Description != "" {
					updates["description"] = params.Description
				}
				if params.Status != "" {
					updates["status"] = params.Status
				}
				if params.Priority != nil {
					updates["priority"] = *params.Priority
				}
				if params.DueDate != "" {
					updates["due_date"] = params.DueDate
				}

				if len(updates) == 0 {
					return nil, fmt.Errorf("no fields to update — provide at least one of: name, description, status, priority, due_date")
				}

				task, err := c.UpdateTask(ctx, params.TaskID, updates)
				if err != nil {
					return nil, err
				}
				return json.Marshal(task)
			},
		},
		{
			name:        "clickup_post_comment",
			description: "Post a comment on a ClickUp task. Use this to report findings, ask questions, or provide status updates.",
			parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string","description":"The ClickUp task ID"},"text":{"type":"string","description":"Comment text (supports markdown)"}},"required":["task_id","text"]}`),
			client:      client,
			execute: func(ctx context.Context, args json.RawMessage, c ClickUpClient) (json.RawMessage, error) {
				var params struct {
					TaskID string `json:"task_id"`
					Text   string `json:"text"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("parsing args: %w", err)
				}
				comment, err := c.PostComment(ctx, params.TaskID, params.Text)
				if err != nil {
					return nil, err
				}
				return json.Marshal(comment)
			},
		},
		{
			name:        "clickup_search_tasks",
			description: "Search for tasks in the ClickUp workspace. Filter by status, tags, assignees, list, or space. Returns matching task summaries.",
			parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"statuses":{"type":"array","items":{"type":"string"},"description":"Filter by status names"},
					"tags":{"type":"array","items":{"type":"string"},"description":"Filter by tag names"},
					"assignees":{"type":"array","items":{"type":"string"},"description":"Filter by assignee user IDs"},
					"list_ids":{"type":"array","items":{"type":"string"},"description":"Filter by list IDs"},
					"space_ids":{"type":"array","items":{"type":"string"},"description":"Filter by space IDs"},
					"include_closed":{"type":"boolean","description":"Include closed tasks (default false)"}
				}
			}`),
			client: client,
			execute: func(ctx context.Context, args json.RawMessage, c ClickUpClient) (json.RawMessage, error) {
				var params struct {
					Statuses      []string `json:"statuses"`
					Tags          []string `json:"tags"`
					Assignees     []string `json:"assignees"`
					ListIDs       []string `json:"list_ids"`
					SpaceIDs      []string `json:"space_ids"`
					IncludeClosed bool     `json:"include_closed"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("parsing args: %w", err)
				}

				opts := &clickup.TaskListOpts{
					OrderBy:       "updated",
					Reverse:       true,
					Statuses:      params.Statuses,
					Tags:          params.Tags,
					Assignees:     params.Assignees,
					ListIDs:       params.ListIDs,
					SpaceIDs:      params.SpaceIDs,
					IncludeClosed: params.IncludeClosed,
					Subtasks:      true,
				}

				tasks, err := c.GetTeamTasks(ctx, opts)
				if err != nil {
					return nil, err
				}

				// Return summaries to keep response manageable
				summaries := make([]map[string]interface{}, 0, len(tasks))
				for _, t := range tasks {
					summary := map[string]interface{}{
						"id":           t.ID,
						"name":         t.Name,
						"status":       t.Status.Status,
						"date_updated": t.DateUpdated,
						"url":          t.URL,
					}
					if len(t.Assignees) > 0 {
						names := make([]string, 0, len(t.Assignees))
						for _, a := range t.Assignees {
							names = append(names, a.Username)
						}
						summary["assignees"] = names
					}
					if len(t.Tags) > 0 {
						tagNames := make([]string, 0, len(t.Tags))
						for _, tag := range t.Tags {
							tagNames = append(tagNames, tag.Name)
						}
						summary["tags"] = tagNames
					}
					summaries = append(summaries, summary)
				}

				return json.Marshal(map[string]interface{}{
					"tasks": summaries,
					"count": len(summaries),
				})
			},
		},
		{
			name:        "clickup_get_attachments",
			description: "Get all attachments for a ClickUp task. Returns file names, URLs, sizes, and MIME types.",
			parameters:  json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string","description":"The ClickUp task ID"}},"required":["task_id"]}`),
			client:      client,
			execute: func(ctx context.Context, args json.RawMessage, c ClickUpClient) (json.RawMessage, error) {
				var params struct {
					TaskID string `json:"task_id"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, fmt.Errorf("parsing args: %w", err)
				}
				attachments, err := c.GetAttachments(ctx, params.TaskID)
				if err != nil {
					return nil, err
				}
				return json.Marshal(map[string]interface{}{
					"attachments": attachments,
					"count":       len(attachments),
				})
			},
		},
	}

	for i := range tools {
		if err := registry.Register(&tools[i]); err != nil {
			return err
		}
	}
	return nil
}
