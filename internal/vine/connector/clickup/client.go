package clickup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
)

const (
	baseURL    = "https://api.clickup.com/api/v2"
	userAgent  = "ivy-vine/1.0"
	maxRetries = 3
	retryBase  = 2 * time.Second
)

// APIError represents an error returned by the ClickUp API.
type APIError struct {
	StatusCode int
	ErrorCode  string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("clickup API error: %d %s: %s", e.StatusCode, e.ErrorCode, e.Message)
}

// Task represents a ClickUp task.
type Task struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      Status `json:"status"`
	Description string `json:"description"`
	DateCreated string `json:"date_created"`
	DateUpdated string `json:"date_updated"`
	Creator     User   `json:"creator"`
	Assignees   []User `json:"assignees"`
	Tags        []Tag  `json:"tags"`
	Priority    *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"priority"`
	DueDate string  `json:"due_date"`
	List    *List   `json:"list"`
	Folder  *Folder `json:"folder"`
	Space   *Space  `json:"space"`
	URL     string  `json:"url"`
}

// Status represents a task status.
type Status struct {
	Status string `json:"status"`
	Color  string `json:"color"`
	Type   string `json:"type"`
}

// User represents a ClickUp user.
type User struct {
	ID             int    `json:"id"`
	Username       string `json:"username"`
	Email          string `json:"email"`
	Initials       string `json:"initials"`
	ProfilePicture string `json:"profilePicture"`
}

// Tag represents a ClickUp tag.
type Tag struct {
	Name  string `json:"name"`
	TagFg string `json:"tag_fg"`
	TagBg string `json:"tag_bg"`
}

// List represents a ClickUp list.
type List struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Folder represents a ClickUp folder.
type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Space represents a ClickUp space.
type Space struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Comment represents a ClickUp task comment.
type Comment struct {
	ID          json.Number `json:"id"` // ClickUp is inconsistent: sometimes string, sometimes number
	TaskID      json.Number `json:"task_id"`
	User        User        `json:"user"`
	CommentText string      `json:"comment_text"`
	Date        json.Number `json:"date"` // Unix ms — can be string or number
}

// Attachment represents a ClickUp task attachment.
type Attachment struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Date     string `json:"date"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

// tasksResponse is the API response for GetTeamTasks.
type tasksResponse struct {
	Tasks    []Task `json:"tasks"`
	LastPage bool   `json:"last_page"`
}

// commentsResponse is the API response for GetComments.
type commentsResponse struct {
	Comments []Comment `json:"comments"`
}

// attachmentsResponse is the API response for GetAttachments.
type attachmentsResponse struct {
	Attachments []Attachment `json:"attachments"`
}

// Client is the ClickUp API client.
type Client struct {
	httpClient *http.Client
	token      string
	teamID     string
	baseURL    string // defaults to production, overridable for tests
	logger     *slog.Logger
}

// NewClient creates a new ClickUp API client from config.
func NewClient(cfg config.ClickUpConfig, logger *slog.Logger) (*Client, error) {
	if cfg.APIToken == "" {
		return nil, fmt.Errorf("clickup api_token is required")
	}

	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err != nil {
			return nil, fmt.Errorf("parsing clickup proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		logger.Info("clickup client using proxy", "proxy", cfg.Proxy)
	}

	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		token:   cfg.APIToken,
		teamID:  cfg.TeamID,
		baseURL: baseURL,
		logger:  logger,
	}, nil
}

// GetTeamTasks fetches tasks for the configured team with optional filters.
func (c *Client) GetTeamTasks(ctx context.Context, opts *TaskListOpts) ([]Task, error) {
	path := fmt.Sprintf("/team/%s/task", c.teamID)
	if opts != nil {
		path += opts.Query()
	}

	var resp tasksResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("getting team tasks: %w", err)
	}
	return resp.Tasks, nil
}

// GetTask fetches a single task by ID.
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	path := fmt.Sprintf("/task/%s", taskID)
	var task Task
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &task); err != nil {
		return nil, fmt.Errorf("getting task %s: %w", taskID, err)
	}
	return &task, nil
}

// UpdateTask updates a task's properties.
func (c *Client) UpdateTask(ctx context.Context, taskID string, updates map[string]interface{}) (*Task, error) {
	path := fmt.Sprintf("/task/%s", taskID)
	body, err := json.Marshal(updates)
	if err != nil {
		return nil, fmt.Errorf("marshaling update body: %w", err)
	}

	var task Task
	if err := c.doRequest(ctx, http.MethodPut, path, body, &task); err != nil {
		return nil, fmt.Errorf("updating task %s: %w", taskID, err)
	}
	return &task, nil
}

// AddCommentReaction adds an emoji reaction to a comment.
func (c *Client) AddCommentReaction(ctx context.Context, commentID json.Number, emoji string) error {
	id, _ := commentID.Int64()
	path := fmt.Sprintf("/comment/%d/reaction", id)
	body, err := json.Marshal(map[string]interface{}{"reactions": []string{emoji}})
	if err != nil {
		return fmt.Errorf("marshaling reaction body: %w", err)
	}
	if err := c.doRequest(ctx, http.MethodPost, path, body, nil); err != nil {
		return fmt.Errorf("adding reaction to comment %s: %w", commentID.String(), err)
	}
	return nil
}

// PostComment adds a comment to a task.
func (c *Client) PostComment(ctx context.Context, taskID string, text string) (*Comment, error) {
	path := fmt.Sprintf("/task/%s/comment", taskID)
	body, err := json.Marshal(map[string]string{"comment_text": text})
	if err != nil {
		return nil, fmt.Errorf("marshaling comment body: %w", err)
	}

	var comment Comment
	if err := c.doRequest(ctx, http.MethodPost, path, body, &comment); err != nil {
		return nil, fmt.Errorf("posting comment to task %s: %w", taskID, err)
	}
	return &comment, nil
}

// ReplyToComment posts a threaded reply to an existing comment.
func (c *Client) ReplyToComment(ctx context.Context, commentID json.Number, text string) (*Comment, error) {
	id, _ := commentID.Int64()
	path := fmt.Sprintf("/comment/%d/reply", id)
	body, err := json.Marshal(map[string]interface{}{"comment_text": text, "notify_all": false})
	if err != nil {
		return nil, fmt.Errorf("marshaling reply body: %w", err)
	}

	var comment Comment
	if err := c.doRequest(ctx, http.MethodPost, path, body, &comment); err != nil {
		return nil, fmt.Errorf("replying to comment %s: %w", commentID.String(), err)
	}
	return &comment, nil
}

// GetComments fetches all comments for a task.
func (c *Client) GetComments(ctx context.Context, taskID string) ([]Comment, error) {
	path := fmt.Sprintf("/task/%s/comment", taskID)
	var resp commentsResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("getting comments for task %s: %w", taskID, err)
	}
	return resp.Comments, nil
}

// GetCommentReplies fetches all replies to a specific comment (thread).
func (c *Client) GetCommentReplies(ctx context.Context, commentID json.Number) ([]Comment, error) {
	id, _ := commentID.Int64()
	path := fmt.Sprintf("/comment/%d/reply", id)
	var resp commentsResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("getting replies for comment %s: %w", commentID.String(), err)
	}
	return resp.Comments, nil
}

// GetAttachments fetches all attachments for a task.
func (c *Client) GetAttachments(ctx context.Context, taskID string) ([]Attachment, error) {
	path := fmt.Sprintf("/task/%s/attachment", taskID)
	var resp attachmentsResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("getting attachments for task %s: %w", taskID, err)
	}
	return resp.Attachments, nil
}

// TaskListOpts provides filtering options for GetTeamTasks.
type TaskListOpts struct {
	Page          int
	OrderBy       string // "created", "updated", "due_date"
	Reverse       bool
	Statuses      []string
	ListIDs       []string
	SpaceIDs      []string
	Tags          []string
	Assignees     []string
	DateUpdatedGT int64 // Unix timestamp in ms
	IncludeClosed bool
	Subtasks      bool
}

// Query builds the query string for task list options.
func (o *TaskListOpts) Query() string {
	if o == nil {
		return ""
	}
	params := url.Values{}

	if o.Page > 0 {
		params.Set("page", strconv.Itoa(o.Page))
	}
	if o.OrderBy != "" {
		params.Set("order_by", o.OrderBy)
	}
	if o.Reverse {
		params.Set("reverse", "true")
	}
	for _, s := range o.Statuses {
		params.Add("statuses[]", s)
	}
	for _, id := range o.ListIDs {
		params.Add("list_ids[]", id)
	}
	for _, id := range o.SpaceIDs {
		params.Add("space_ids[]", id)
	}
	for _, t := range o.Tags {
		params.Add("tags[]", t)
	}
	for _, a := range o.Assignees {
		params.Add("assignees[]", a)
	}
	if o.DateUpdatedGT > 0 {
		params.Set("date_updated_gt", strconv.FormatInt(o.DateUpdatedGT, 10))
	}
	if o.IncludeClosed {
		params.Set("include_closed", "true")
	}
	if o.Subtasks {
		params.Set("subtasks", "true")
	}

	q := params.Encode()
	if q == "" {
		return ""
	}
	return "?" + q
}

// doRequest executes an HTTP request against the ClickUp API with retry logic.
func (c *Client) doRequest(ctx context.Context, method, path string, body []byte, result interface{}) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := retryBase * time.Duration(attempt)
			c.logger.Debug("retrying clickup API request", "attempt", attempt, "wait", wait, "path", path)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}

		lastErr = c.execRequest(ctx, method, path, body, result)
		if lastErr == nil {
			return nil
		}

		// Don't retry on 4xx errors (except 429)
		if apiErr, ok := lastErr.(*APIError); ok && apiErr.StatusCode != 429 && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			return lastErr
		}
	}

	return lastErr
}

// execRequest executes a single HTTP request.
func (c *Client) execRequest(ctx context.Context, method, path string, body []byte, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			ErrCode string `json:"err_code"`
			Message string `json:"message"`
			ECODE   string `json:"ECODE"`
		}
		_ = json.Unmarshal(respBody, &errResp)

		errCode := errResp.ErrCode
		if errCode == "" {
			errCode = errResp.ECODE
		}

		return &APIError{
			StatusCode: resp.StatusCode,
			ErrorCode:  errCode,
			Message:    string(respBody),
		}
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decoding response: %w (body: %s)", err, string(respBody[:min(len(respBody), 200)]))
		}
	}

	return nil
}
