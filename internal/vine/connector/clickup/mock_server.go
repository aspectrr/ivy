package clickup

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aspectrr/ivy/internal/vine/config"
)

// MockServer is a fake ClickUp API server for local development and testing.
// It simulates tasks, comments, threads, reactions, and replies.
type MockServer struct {
	mu      sync.RWMutex
	server  *httptest.Server
	logger  *slog.Logger
	tasks   map[string]*Task
	comments map[string][]Comment // taskID → comments
	replies  map[string][]Comment // parentCommentID → replies
	reactions map[string][]string // commentID → emoji list

	nextCommentID int64
}

// NewMockServer creates a mock ClickUp API server.
func NewMockServer(logger *slog.Logger) *MockServer {
	ms := &MockServer{
		logger:       logger,
		tasks:        make(map[string]*Task),
		comments:     make(map[string][]Comment),
		replies:      make(map[string][]Comment),
		reactions:    make(map[string][]string),
		nextCommentID: 90000000000001,
	}

	mux := http.NewServeMux()
	ms.server = httptest.NewServer(mux)

	// Register routes
	mux.HandleFunc("/", ms.handleRequest)

	return ms
}

// URL returns the mock server's base URL.
func (ms *MockServer) URL() string {
	return ms.server.URL
}

// Close shuts down the mock server.
func (ms *MockServer) Close() {
	ms.server.Close()
}

// AddTask adds a mock task to the server.
func (ms *MockServer) AddTask(task Task) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.tasks[task.ID] = &task
}

// PostMention simulates a user posting a comment that @mentions the agent.
// Returns the comment ID. This is used to test the poller's mention detection.
func (ms *MockServer) PostMention(taskID, author, text string) string {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	id := ms.nextCommentID
	ms.nextCommentID++

	comment := Comment{
		ID:          json.Number(fmt.Sprintf("%d", id)),
		TaskID:      json.Number("0"),
		User:        User{ID: 1, Username: author, Email: author + "@test.com"},
		CommentText: text,
		Date:        json.Number(fmt.Sprintf("%d", time.Now().UnixMilli())),
	}

	ms.comments[taskID] = append(ms.comments[taskID], comment)
	ms.logger.Info("mock: mention posted", "task_id", taskID, "author", author, "comment_id", id)
	return comment.ID.String()
}

// PostThreadReply simulates a user replying in a thread.
func (ms *MockServer) PostThreadReply(parentCommentID, author, text string) string {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	id := ms.nextCommentID
	ms.nextCommentID++

	reply := Comment{
		ID:          json.Number(fmt.Sprintf("%d", id)),
		TaskID:      json.Number("0"),
		User:        User{ID: 1, Username: author, Email: author + "@test.com"},
		CommentText: text,
		Date:        json.Number(fmt.Sprintf("%d", time.Now().UnixMilli())),
	}

	ms.replies[parentCommentID] = append(ms.replies[parentCommentID], reply)
	ms.logger.Info("mock: thread reply posted", "parent", parentCommentID, "author", author, "reply_id", id)
	return reply.ID.String()
}

// GetReactions returns reactions on a comment.
func (ms *MockServer) GetReactions(commentID string) []string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.reactions[commentID]
}

// GetComments returns all comments on a task.
func (ms *MockServer) GetCommentsForTask(taskID string) []Comment {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.comments[taskID]
}

// GetRepliesForComment returns all replies in a thread.
func (ms *MockServer) GetRepliesForComment(parentID string) []Comment {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.replies[parentID]
}

// handleRequest routes all incoming requests.
func (ms *MockServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	ms.logger.Debug("mock request", "method", r.Method, "path", r.URL.Path)

	path := r.URL.Path

	switch {
	// GET /team/{id}/task — list tasks
	case r.Method == http.MethodGet && strings.Contains(path, "/task") && strings.Contains(path, "/team/"):
		ms.handleGetTasks(w, r)

	// GET /task/{id} — single task
	case r.Method == http.MethodGet && matchesTaskPath(path) && !strings.Contains(path, "/comment") && !strings.Contains(path, "/attachment"):
		ms.handleGetTask(w, r, path)

	// GET /task/{id}/comment — list comments
	case r.Method == http.MethodGet && strings.Contains(path, "/comment") && !strings.Contains(path, "/reply") && !strings.Contains(path, "/reaction") && isTaskCommentsPath(path):
		ms.handleGetComments(w, r, path)

	// POST /task/{id}/comment — create comment
	case r.Method == http.MethodPost && isTaskCommentsPath(path):
		ms.handlePostComment(w, r, path)

	// POST /comment/{id}/reply — reply to comment
	case r.Method == http.MethodPost && strings.Contains(path, "/reply"):
		ms.handleReplyToComment(w, r, path)

	// POST /comment/{id}/reaction — add reaction
	case r.Method == http.MethodPost && strings.Contains(path, "/reaction"):
		ms.handleAddReaction(w, r, path)

	// GET /comment/{id}/reply — get replies
	case r.Method == http.MethodGet && strings.Contains(path, "/reply"):
		ms.handleGetReplies(w, r, path)

	default:
		ms.logger.Warn("mock: unhandled request", "method", r.Method, "path", path)
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"err": "not found", "ECODE": "NOT_FOUND"})
	}
}

func (ms *MockServer) handleGetTasks(w http.ResponseWriter, r *http.Request) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	tasks := make([]Task, 0, len(ms.tasks))
	for _, t := range ms.tasks {
		tasks = append(tasks, *t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"tasks": tasks, "last_page": true})
}

func (ms *MockServer) handleGetTask(w http.ResponseWriter, r *http.Request, path string) {
	taskID := extractTaskID(path)
	if taskID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ms.mu.RLock()
	task, ok := ms.tasks[taskID]
	ms.mu.RUnlock()

	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"err": "task not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (ms *MockServer) handleGetComments(w http.ResponseWriter, r *http.Request, path string) {
	taskID := extractTaskID(path)

	ms.mu.RLock()
	comments := ms.comments[taskID]
	if comments == nil {
		comments = []Comment{}
	}

	result := make([]commentResponse, 0, len(comments))
	for _, c := range comments {
		reactions := ms.reactions[c.ID.String()]
		if reactions == nil {
			reactions = []string{}
		}
		result = append(result, commentToResponse(c, len(ms.replies[c.ID.String()]), reactions))
	}
	ms.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"comments": result})
}

func (ms *MockServer) handlePostComment(w http.ResponseWriter, r *http.Request, path string) {
	taskID := extractTaskID(path)

	var body struct {
		CommentText string `json:"comment_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ms.mu.Lock()
	id := atomic.AddInt64(&ms.nextCommentID, 1)

	comment := Comment{
		ID:          json.Number(fmt.Sprintf("%d", id)),
		TaskID:      json.Number("0"),
		User:        User{ID: 100397806, Username: "Ivy Agent", Email: "agent@test.com"},
		CommentText: body.CommentText,
		Date:        json.Number(fmt.Sprintf("%d", time.Now().UnixMilli())),
	}

	ms.comments[taskID] = append(ms.comments[taskID], comment)
	ms.mu.Unlock()

	ms.logger.Info("mock: agent posted comment", "task_id", taskID, "text", body.CommentText)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(commentToResponse(comment, 0, nil))
}

func (ms *MockServer) handleReplyToComment(w http.ResponseWriter, r *http.Request, path string) {
	parentID := extractCommentID(path)

	var body struct {
		CommentText string `json:"comment_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ms.mu.Lock()
	id := atomic.AddInt64(&ms.nextCommentID, 1)

	reply := Comment{
		ID:          json.Number(fmt.Sprintf("%d", id)),
		TaskID:      json.Number("0"),
		User:        User{ID: 100397806, Username: "Ivy Agent", Email: "agent@test.com"},
		CommentText: body.CommentText,
		Date:        json.Number(fmt.Sprintf("%d", time.Now().UnixMilli())),
	}

	ms.replies[parentID] = append(ms.replies[parentID], reply)
	ms.mu.Unlock()

	ms.logger.Info("mock: agent replied in thread", "parent", parentID, "text", body.CommentText)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(commentToResponse(reply, 0, nil))
}

func (ms *MockServer) handleAddReaction(w http.ResponseWriter, r *http.Request, path string) {
	commentID := extractCommentID(path)

	var body struct {
		Reactions []string `json:"reactions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ms.mu.Lock()
	for _, emoji := range body.Reactions {
		ms.reactions[commentID] = append(ms.reactions[commentID], emoji)
	}
	ms.mu.Unlock()

	ms.logger.Info("mock: reaction added", "comment_id", commentID, "emojis", body.Reactions)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{}`))
}

func (ms *MockServer) handleGetReplies(w http.ResponseWriter, r *http.Request, path string) {
	parentID := extractCommentID(path)

	ms.mu.RLock()
	replies := ms.replies[parentID]
	if replies == nil {
		replies = []Comment{}
	}

	result := make([]commentResponse, 0, len(replies))
	for _, c := range replies {
		result = append(result, commentToResponse(c, 0, nil))
	}
	ms.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"comments": result})
}

// --- Helpers ---

// commentResponse mirrors the ClickUp API response for a single comment.
type commentResponse struct {
	ID          json.Number    `json:"id"`
	TaskID      json.Number    `json:"task_id"`
	User        User           `json:"user"`
	CommentText string         `json:"comment_text"`
	Date        json.Number    `json:"date"`
	ReplyCount  int            `json:"reply_count"`
	Reactions   []reactionResp `json:"reactions,omitempty"`
}

type reactionResp struct {
	Reaction string `json:"reaction"`
	Date     string `json:"date"`
	User     User   `json:"user"`
}

func commentToResponse(c Comment, replyCount int, reactions []string) commentResponse {
	cr := commentResponse{
		ID:          c.ID,
		TaskID:      c.TaskID,
		User:        c.User,
		CommentText: c.CommentText,
		Date:        c.Date,
		ReplyCount:  replyCount,
	}
	for _, e := range reactions {
		cr.Reactions = append(cr.Reactions, reactionResp{
			Reaction: e,
			Date:     c.Date.String(),
			User:     User{ID: 100397806, Username: "Ivy Agent"},
		})
	}
	return cr
}

func extractTaskID(path string) string {
	// /task/{id}/comment or /task/{id}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, p := range parts {
		if p == "task" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func extractCommentID(path string) string {
	// /comment/{id}/reply or /comment/{id}/reaction
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, p := range parts {
		if p == "comment" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func matchesTaskPath(path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, p := range parts {
		if p == "task" && i+1 < len(parts) {
			return true
		}
	}
	return false
}

func isTaskCommentsPath(path string) bool {
	return strings.Contains(path, "/task/") && strings.HasSuffix(path, "/comment")
}

// NewMockClient creates a ClickUp Client pointing at the mock server.
// This is useful for integration tests and local development.
func NewMockClient(ms *MockServer, cfg config.ClickUpConfig, logger *slog.Logger) *Client {
	return &Client{
		httpClient: http.DefaultClient,
		token:      "mock-token",
		teamID:     cfg.TeamID,
		baseURL:    ms.URL(),
		logger:     logger,
	}
}
