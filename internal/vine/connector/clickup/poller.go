package clickup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
)

// TriggerReason describes why a task was picked up by the poller.
type TriggerReason string

const (
	ReasonAssigned  TriggerReason = "assigned"  // Task assigned to the agent user
	ReasonMentioned TriggerReason = "mentioned" // Agent @mentioned in a comment
	ReasonUpdated   TriggerReason = "updated"   // Task updated (generic)
	ReasonCreated   TriggerReason = "created"   // New task created
)

// MentionInfo contains details about an @mention that triggered the handler.
type MentionInfo struct {
	CommentID   string // ClickUp comment ID
	CommentText string // Full comment text
	Author      string // Username of the person who mentioned
	Date        string // Comment date
}

// TaskHandler is called for each task event found by the poller.
// The handler receives the task, whether it's new, the trigger reason,
// and optional mention info when the trigger is ReasonMentioned.
type TaskHandler func(task Task, isNew bool, reason TriggerReason, mention *MentionInfo)

// Poller polls the ClickUp API for updated tasks and comments, calling
// the handler for new assignments, @mentions, and task updates.
type Poller struct {
	client      *Client
	cfg         config.ClickUpConfig
	handler     TaskHandler
	logger      *slog.Logger
	lastUpdated int64 // Unix ms of last processed task
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
}

// NewPoller creates a new task poller.
func NewPoller(client *Client, cfg config.ClickUpConfig, handler TaskHandler, logger *slog.Logger) *Poller {
	return &Poller{
		client:  client,
		cfg:     cfg,
		handler: handler,
		logger:  logger,
		done:    make(chan struct{}),
	}
}

// Start begins the polling loop in a background goroutine.
func (p *Poller) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)

	// Initialize lastUpdated to now so we only process tasks updated from this point
	p.mu.Lock()
	p.lastUpdated = time.Now().UnixMilli()
	p.mu.Unlock()

	go p.run(ctx)
}

// Stop gracefully stops the poller and waits for the current poll to finish.
func (p *Poller) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	<-p.done
}

// LastUpdated returns the timestamp of the last processed task.
func (p *Poller) LastUpdated() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastUpdated
}

func (p *Poller) run(ctx context.Context) {
	defer close(p.done)

	interval := p.cfg.PollInterval
	if interval == 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	p.logger.Info("clickup poller started",
		"interval", interval,
		"team_id", p.cfg.TeamID,
		"agent_username", p.cfg.AgentUsername,
	)

	// Do an initial poll
	p.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("clickup poller stopped")
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *Poller) poll(ctx context.Context) {
	p.mu.Lock()
	since := p.lastUpdated
	p.mu.Unlock()

	opts := &TaskListOpts{
		OrderBy:       "updated",
		Reverse:       true,
		DateUpdatedGT: since,
		Subtasks:      true,
	}

	// Apply configured filters
	if p.cfg.ListID != "" {
		opts.ListIDs = []string{p.cfg.ListID}
	}
	if p.cfg.SpaceID != "" {
		opts.SpaceIDs = []string{p.cfg.SpaceID}
	}
	if p.cfg.Tag != "" {
		opts.Tags = []string{p.cfg.Tag}
	}
	if p.cfg.Assignee != "" {
		opts.Assignees = []string{p.cfg.Assignee}
	}

	tasks, err := p.client.GetTeamTasks(ctx, opts)
	if err != nil {
		p.logger.Error("clickup poll failed", "error", err)
		return
	}

	if len(tasks) == 0 {
		return
	}

	// Process tasks and track the newest update timestamp
	var newestUpdated int64
	for _, task := range tasks {
		updated := parseTimestamp(task.DateUpdated)
		if updated > newestUpdated {
			newestUpdated = updated
		}

		isNew := parseTimestamp(task.DateCreated) > since

		// Determine trigger reason
		if isNew {
			p.handler(task, true, ReasonCreated, nil)
		} else if p.isAssignedToAgent(task) {
			p.handler(task, false, ReasonAssigned, nil)
		} else {
			p.handler(task, false, ReasonUpdated, nil)
		}

		// Check for @mentions in comments
		if p.cfg.AgentUsername != "" {
			p.checkMentions(ctx, task, since)
		}
	}

	p.mu.Lock()
	if newestUpdated > p.lastUpdated {
		p.lastUpdated = newestUpdated
	}
	p.mu.Unlock()

	p.logger.Debug("clickup poll completed", "tasks_processed", len(tasks))
}

// isAssignedToAgent checks if the task is assigned to the configured agent user.
func (p *Poller) isAssignedToAgent(task Task) bool {
	if p.cfg.Assignee == "" {
		return false
	}
	for _, a := range task.Assignees {
		if fmt.Sprintf("%d", a.ID) == p.cfg.Assignee || a.Username == p.cfg.AgentUsername {
			return true
		}
	}
	return false
}

// checkMentions fetches comments for a task and looks for @mentions of the agent username.
func (p *Poller) checkMentions(ctx context.Context, task Task, since int64) {
	comments, err := p.client.GetComments(ctx, task.ID)
	if err != nil {
		p.logger.Debug("failed to fetch comments for task",
			"task_id", task.ID,
			"error", err,
		)
		return
	}

	mentionPattern := "@" + p.cfg.AgentUsername

	for _, comment := range comments {
		commentDate := parseTimestamp(comment.Date)

		// Only look at comments newer than our last poll
		if commentDate <= since {
			continue
		}

		// Check for @mention (case-insensitive)
		if !containsMention(comment.CommentText, mentionPattern) {
			continue
		}

		p.logger.Info("agent mentioned in comment",
			"task_id", task.ID,
			"comment_id", comment.ID,
			"author", comment.User.Username,
		)

		p.handler(task, false, ReasonMentioned, &MentionInfo{
			CommentID:   comment.ID,
			CommentText: comment.CommentText,
			Author:      comment.User.Username,
			Date:        comment.Date,
		})
	}
}

// containsMention checks if text contains a mention pattern (case-insensitive).
// Supports formats like @username, @Username, @User Name (with spaces for display names).
func containsMention(text, pattern string) bool {
	lower := strings.ToLower(text)
	lowerPattern := strings.ToLower(pattern)
	return strings.Contains(lower, lowerPattern)
}

// parseTimestamp parses a ClickUp Unix ms timestamp string.
func parseTimestamp(s string) int64 {
	if s == "" {
		return 0
	}
	var ms int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			ms = ms*10 + int64(c-'0')
		}
	}
	return ms
}
