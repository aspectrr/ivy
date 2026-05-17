package clickup

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
)

// TaskHandler is called for each new or updated task found by the poller.
// The handler receives the task and whether it's a new task (true) or
// an update to an existing task (false).
type TaskHandler func(task Task, isNew bool)

// Poller polls the ClickUp API for updated tasks and calls the handler.
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

	p.logger.Info("clickup poller started", "interval", interval, "team_id", p.cfg.TeamID)

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

		// Determine if new or updated relative to our last poll
		isNew := parseTimestamp(task.DateCreated) > since
		p.handler(task, isNew)
	}

	p.mu.Lock()
	if newestUpdated > p.lastUpdated {
		p.lastUpdated = newestUpdated
	}
	p.mu.Unlock()

	p.logger.Debug("clickup poll completed", "tasks_processed", len(tasks))
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
