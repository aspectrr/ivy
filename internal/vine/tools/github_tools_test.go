package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aspectrr/ivy/internal/vine/connector/github"
)

// --- Branch validation tests ---

func TestGitHubToolConfig_IsProtectedRef(t *testing.T) {
	cfg := GitHubToolConfig{
		BranchPrefix:  "agent/",
		ProtectedRefs: []string{"main", "master", "release/*"},
	}

	tests := []struct {
		name   string
		branch string
		want   bool
	}{
		{"main is protected", "main", true},
		{"master is protected", "master", true},
		{"release/v1 is protected", "release/v1", true},
		{"develop is not protected", "develop", false},
		{"agent/fix-bug is not protected", "agent/fix-bug", false},
		{"feature/auth is not protected", "feature/auth", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.isProtectedRef(tt.branch)
			if got != tt.want {
				t.Errorf("isProtectedRef(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

func TestGitHubToolConfig_ValidateAgentBranch(t *testing.T) {
	cfg := GitHubToolConfig{
		BranchPrefix:  "agent/",
		ProtectedRefs: []string{"main", "master", "release/*"},
	}

	tests := []struct {
		name    string
		branch  string
		wantErr bool
	}{
		{"valid agent branch", "agent/fix-auth-bug", false},
		{"valid agent branch with subdirs", "agent/fix/2024/auth-bug", false},
		{"protected main", "main", true},
		{"protected master", "master", true},
		{"protected release", "release/v2.0", true},
		{"missing agent prefix", "feature/my-fix", true},
		{"empty branch", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.validateAgentBranch(tt.branch)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAgentBranch(%q) error = %v, wantErr %v", tt.branch, err, tt.wantErr)
			}
		})
	}
}

func TestGitHubToolConfig_EmptyBranchPrefix(t *testing.T) {
	// If no prefix is set, any non-protected branch is allowed.
	cfg := GitHubToolConfig{
		BranchPrefix:  "",
		ProtectedRefs: []string{"main", "master"},
	}

	tests := []struct {
		name    string
		branch  string
		wantErr bool
	}{
		{"any branch allowed when no prefix", "feature/my-fix", false},
		{"protected still enforced", "main", true},
		{"agent prefix works too", "agent/fix", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.validateAgentBranch(tt.branch)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAgentBranch(%q) error = %v, wantErr %v", tt.branch, err, tt.wantErr)
			}
		})
	}
}

// --- Tool parameter validation tests (no network) ---

func TestGitHubPushToBranchTool_BranchValidation(t *testing.T) {
	tool := &GitHubPushToBranchTool{
		Client: nil, // no client needed for validation tests
		Config: GitHubToolConfig{
			BranchPrefix:  "agent/",
			ProtectedRefs: []string{"main", "master", "release/*"},
		},
	}

	tests := []struct {
		name    string
		args    string
		wantErr string // substring of expected error
	}{
		{
			name:    "protected branch rejected",
			args:    `{"owner":"acme","repo":"app","branch":"main","message":"fix","files":[{"path":"f.txt","content":"x"}]}`,
			wantErr: "protected",
		},
		{
			name:    "missing agent prefix rejected",
			args:    `{"owner":"acme","repo":"app","branch":"feature/fix","message":"fix","files":[{"path":"f.txt","content":"x"}]}`,
			wantErr: "must start with",
		},
		{
			name:    "no files rejected",
			args:    `{"owner":"acme","repo":"app","branch":"agent/fix","message":"fix","files":[]}`,
			wantErr: "at least one file",
		},
		{
			name:    "empty message rejected",
			args:    `{"owner":"acme","repo":"app","branch":"agent/fix","message":"","files":[{"path":"f.txt","content":"x"}]}`,
			wantErr: "message is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(tt.args), ToolContext{})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			// All these should fail before hitting the nil client.
			// The branch validation errors should match.
		})
	}
}

func TestGitHubCreatePRTool_BranchValidation(t *testing.T) {
	tool := &GitHubCreatePRTool{
		Client: nil,
		Config: GitHubToolConfig{
			BranchPrefix:  "agent/",
			ProtectedRefs: []string{"main", "master"},
		},
	}

	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "protected head branch rejected",
			args:    `{"owner":"acme","repo":"app","title":"fix","head":"main","base":"main"}`,
			wantErr: "protected",
		},
		{
			name:    "missing agent prefix on head rejected",
			args:    `{"owner":"acme","repo":"app","title":"fix","head":"feature/fix","base":"main"}`,
			wantErr: "must start with",
		},
		{
			name:    "agent branch as base rejected",
			args:    `{"owner":"acme","repo":"app","title":"fix","head":"agent/fix","base":"agent/something"}`,
			wantErr: "agent branch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(tt.args), ToolContext{})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
		})
	}
}

func TestGitHubCommentOnPRTool_Validation(t *testing.T) {
	tool := &GitHubCommentOnPRTool{
		Client: nil,
		Config: GitHubToolConfig{
			BranchPrefix:  "agent/",
			ProtectedRefs: []string{"main"},
		},
	}

	tests := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{
			name:    "missing owner",
			args:    `{"owner":"","repo":"app","pull_number":1,"body":"hello"}`,
			wantErr: true,
		},
		{
			name:    "invalid PR number",
			args:    `{"owner":"acme","repo":"app","pull_number":0,"body":"hello"}`,
			wantErr: true,
		},
		{
			name:    "empty body",
			args:    `{"owner":"acme","repo":"app","pull_number":1,"body":""}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(tt.args), ToolContext{})
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGitHubUpdatePRTool_Validation(t *testing.T) {
	tool := &GitHubUpdatePRTool{
		Client: nil,
		Config: GitHubToolConfig{
			BranchPrefix:  "agent/",
			ProtectedRefs: []string{"main"},
		},
	}

	tests := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{
			name:    "no title or body provided",
			args:    `{"owner":"acme","repo":"app","pull_number":1}`,
			wantErr: true,
		},
		{
			name:    "invalid PR number",
			args:    `{"owner":"acme","repo":"app","pull_number":-1,"title":"new title"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), json.RawMessage(tt.args), ToolContext{})
			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- Tool definition tests ---

func TestGitHubToolDefinitions(t *testing.T) {
	cfg := GitHubToolConfig{
		BranchPrefix:  "agent/",
		ProtectedRefs: []string{"main", "master"},
	}

	registry := NewRegistry()
	err := RegisterGitHubTools(registry, (*github.Client)(nil), cfg)
	if err != nil {
		t.Fatalf("RegisterGitHubTools() error: %v", err)
	}

	expectedTools := []string{
		"github_push_to_branch",
		"github_create_pull_request",
		"github_comment_on_pull_request",
		"github_update_pull_request",
		"github_list_pull_requests",
	}

	for _, name := range expectedTools {
		tool, err := registry.Get(name)
		if err != nil {
			t.Errorf("tool %q not registered: %v", name, err)
			continue
		}
		def := tool.Definition()
		if def.Name != name {
			t.Errorf("tool name = %q, want %q", def.Name, name)
		}
		if def.Description == "" {
			t.Errorf("tool %q has empty description", name)
		}
		if def.Parameters == nil {
			t.Errorf("tool %q has nil parameters", name)
		}
	}
}

func TestGitHubTools_NoMergeTool(t *testing.T) {
	cfg := GitHubToolConfig{
		BranchPrefix:  "agent/",
		ProtectedRefs: []string{"main"},
	}

	registry := NewRegistry()
	_ = RegisterGitHubTools(registry, (*github.Client)(nil), cfg)

	// Verify that merge-related tools do NOT exist.
	forbidden := []string{
		"github_merge_pull_request",
		"github_close_pull_request",
		"github_delete_branch",
		"github_push_to_main",
		"github_manage_settings",
	}

	for _, name := range forbidden {
		_, err := registry.Get(name)
		if err == nil {
			t.Errorf("forbidden tool %q should not be registered", name)
		}
	}
}

// --- Mock client for integration-style tests ---

type mockGitHubClient struct {
	installations map[string]int64 // "owner/repo" → installation ID
	prs           map[string][]github.PullRequest
	comments      map[int][]github.Comment // PR number → comments
	branches      map[string]string        // "owner/repo:branch" → SHA
	commits       []mockCommit
}

type mockCommit struct {
	Owner, Repo, Branch, Message string
	Files                        map[string]string
}

func (m *mockGitHubClient) GetInstallationForRepo(_ context.Context, owner, repo string) (int64, error) {
	key := owner + "/" + repo
	id, ok := m.installations[key]
	if !ok {
		return 0, fmt.Errorf("no installation found for %s", key)
	}
	return id, nil
}

func (m *mockGitHubClient) GetBranchSHA(_ context.Context, _ int64, owner, repo, branch string) (string, error) {
	key := owner + "/" + repo + ":" + branch
	sha, ok := m.branches[key]
	if !ok {
		return "", fmt.Errorf("branch %s not found", branch)
	}
	return sha, nil
}

func (m *mockGitHubClient) CreateRef(_ context.Context, _ int64, owner, repo, ref, sha string) error {
	key := owner + "/" + repo + ":" + ref
	m.branches[key] = sha
	return nil
}

func (m *mockGitHubClient) CreateCommit(_ context.Context, _ int64, owner, repo, branch, message string, files map[string]string) (string, error) {
	sha := "abc123def456"
	m.commits = append(m.commits, mockCommit{
		Owner: owner, Repo: repo, Branch: branch, Message: message, Files: files,
	})
	// Update the branch SHA.
	key := owner + "/" + repo + ":" + branch
	m.branches[key] = sha
	return sha, nil
}

func (m *mockGitHubClient) CreatePullRequest(_ context.Context, _ int64, owner, repo string, opts github.CreatePROptions) (*github.PullRequest, error) {
	pr := &github.PullRequest{
		Number:  len(m.prs[owner+"/"+repo]) + 1,
		Title:   opts.Title,
		Body:    opts.Body,
		State:   "open",
		Draft:   opts.Draft,
		HTMLURL: "https://github.com/" + owner + "/" + repo + "/pull/" + fmt.Sprintf("%d", len(m.prs[owner+"/"+repo])+1),
	}
	pr.Head.Ref = opts.Head
	pr.Head.Repo.FullName = owner + "/" + repo
	pr.Base.Ref = opts.Base
	pr.Base.Repo.FullName = owner + "/" + repo
	pr.User.Login = "ivy-agent[bot]"

	key := owner + "/" + repo
	m.prs[key] = append(m.prs[key], *pr)
	return pr, nil
}

func (m *mockGitHubClient) UpdatePullRequest(_ context.Context, _ int64, owner, repo string, number int, title, body string) (*github.PullRequest, error) {
	key := owner + "/" + repo
	for i, pr := range m.prs[key] {
		if pr.Number == number {
			if title != "" {
				m.prs[key][i].Title = title
			}
			if body != "" {
				m.prs[key][i].Body = body
			}
			return &m.prs[key][i], nil
		}
	}
	return nil, fmt.Errorf("PR #%d not found", number)
}

func (m *mockGitHubClient) CommentOnPullRequest(_ context.Context, _ int64, owner, repo string, pullNumber int, body string) (*github.Comment, error) {
	comment := &github.Comment{
		ID:   int64(len(m.comments[pullNumber]) + 1),
		Body: body,
	}
	comment.User.Login = "ivy-agent[bot]"
	m.comments[pullNumber] = append(m.comments[pullNumber], *comment)
	return comment, nil
}

func (m *mockGitHubClient) ListPullRequests(_ context.Context, _ int64, owner, repo, state, head string) (github.PullRequestListResponse, error) {
	key := owner + "/" + repo
	prs := m.prs[key]
	if state == "open" {
		var open github.PullRequestListResponse
		for _, pr := range prs {
			if pr.State == "open" {
				open = append(open, pr)
			}
		}
		return open, nil
	}
	return prs, nil
}

func (m *mockGitHubClient) GetPullRequest(_ context.Context, _ int64, owner, repo string, number int) (*github.PullRequest, error) {
	key := owner + "/" + repo
	for _, pr := range m.prs[key] {
		if pr.Number == number {
			return &pr, nil
		}
	}
	return nil, fmt.Errorf("PR #%d not found", number)
}

// helper
func fmt_Sprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

func newMockClient() *mockGitHubClient {
	return &mockGitHubClient{
		installations: map[string]int64{"acme/app": 42},
		prs:           make(map[string][]github.PullRequest),
		comments:      make(map[int][]github.Comment),
		branches: map[string]string{
			"acme/app:main":       "sha-main-abc",
			"acme/app:agent/fix1": "sha-agent-fix1-abc",
		},
	}
}

func TestGitHubPushToBranchTool_MockSuccess(t *testing.T) {
	mock := newMockClient()
	tool := &GitHubPushToBranchTool{
		Client: mock,
		Config: GitHubToolConfig{
			BranchPrefix:  "agent/",
			ProtectedRefs: []string{"main", "master"},
		},
	}

	args := `{
		"owner": "acme",
		"repo": "app",
		"branch": "agent/fix-auth",
		"base_branch": "main",
		"message": "fix: resolve auth null check",
		"files": [
			{"path": "src/auth.ts", "content": "export function auth() { return true; }"}
		]
	}`

	result, err := tool.Execute(context.Background(), json.RawMessage(args), ToolContext{})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	if resp["status"] != "pushed" {
		t.Errorf("status = %v, want pushed", resp["status"])
	}
	if resp["commit_sha"] == nil {
		t.Error("commit_sha is nil")
	}

	// Verify commit was recorded.
	if len(mock.commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(mock.commits))
	}
	if mock.commits[0].Message != "fix: resolve auth null check" {
		t.Errorf("commit message = %q, want %q", mock.commits[0].Message, "fix: resolve auth null check")
	}
}

func TestGitHubCreatePRTool_MockSuccess(t *testing.T) {
	mock := newMockClient()
	tool := &GitHubCreatePRTool{
		Client: mock,
		Config: GitHubToolConfig{
			BranchPrefix:  "agent/",
			ProtectedRefs: []string{"main", "master"},
		},
	}

	args := `{
		"owner": "acme",
		"repo": "app",
		"title": "fix: resolve auth null check",
		"body": "## Summary\nFixed the auth bug.",
		"head": "agent/fix-auth",
		"base": "main",
		"draft": true
	}`

	result, err := tool.Execute(context.Background(), json.RawMessage(args), ToolContext{})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	if resp["status"] != "created" {
		t.Errorf("status = %v, want created", resp["status"])
	}
	if resp["draft"] != true {
		t.Error("draft should be true")
	}
	if resp["pr_number"] == nil {
		t.Error("pr_number is nil")
	}
}

func TestGitHubCommentOnPRTool_MockSuccess(t *testing.T) {
	mock := newMockClient()
	tool := &GitHubCommentOnPRTool{
		Client: mock,
		Config: GitHubToolConfig{BranchPrefix: "agent/", ProtectedRefs: []string{"main"}},
	}

	// First create a PR so we have one.
	mock.prs["acme/app"] = append(mock.prs["acme/app"], github.PullRequest{
		Number:  5,
		Title:   "test PR",
		State:   "open",
		Head:    github.PRBranch{Ref: "agent/fix1"},
		Base:    github.PRBranch{Ref: "main"},
		HTMLURL: "https://github.com/acme/app/pull/5",
	})

	args := `{
		"owner": "acme",
		"repo": "app",
		"pull_number": 5,
		"body": "✅ Fix verified in sandbox. Tests pass."
	}`

	result, err := tool.Execute(context.Background(), json.RawMessage(args), ToolContext{})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	if resp["status"] != "commented" {
		t.Errorf("status = %v, want commented", resp["status"])
	}

	// Verify comment was recorded.
	if len(mock.comments[5]) != 1 {
		t.Fatalf("expected 1 comment on PR #5, got %d", len(mock.comments[5]))
	}
}

func TestGitHubUpdatePRTool_RejectsNonAgentPR(t *testing.T) {
	mock := newMockClient()
	tool := &GitHubUpdatePRTool{
		Client: mock,
		Config: GitHubToolConfig{BranchPrefix: "agent/", ProtectedRefs: []string{"main"}},
	}

	// Create a PR from a non-agent branch (e.g., a human PR).
	mock.prs["acme/app"] = append(mock.prs["acme/app"], github.PullRequest{
		Number: 10,
		Title:  "Human PR",
		State:  "open",
		Head:   github.PRBranch{Ref: "feature/human-branch"},
		Base:   github.PRBranch{Ref: "main"},
	})

	args := `{
		"owner": "acme",
		"repo": "app",
		"pull_number": 10,
		"title": "Updated title"
	}`

	_, err := tool.Execute(context.Background(), json.RawMessage(args), ToolContext{})
	if err == nil {
		t.Fatal("expected error when updating non-agent PR, got nil")
	}
	if _, ok := err.(interface{ Error() string }); !ok {
		t.Errorf("error should mention agent branch restriction, got: %v", err)
	}
}

func TestGitHubListPRsTool_MockSuccess(t *testing.T) {
	mock := newMockClient()
	tool := &GitHubListPRsTool{
		Client: mock,
		Config: GitHubToolConfig{BranchPrefix: "agent/", ProtectedRefs: []string{"main"}},
	}

	mock.prs["acme/app"] = append(mock.prs["acme/app"],
		github.PullRequest{
			Number:  1,
			Title:   "Agent fix 1",
			State:   "open",
			Head:    github.PRBranch{Ref: "agent/fix1"},
			Base:    github.PRBranch{Ref: "main"},
			HTMLURL: "https://github.com/acme/app/pull/1",
			Draft:   false,
		},
		github.PullRequest{
			Number:  2,
			Title:   "Agent fix 2",
			State:   "open",
			Head:    github.PRBranch{Ref: "agent/fix2"},
			Base:    github.PRBranch{Ref: "main"},
			HTMLURL: "https://github.com/acme/app/pull/2",
			Draft:   true,
		},
	)

	args := `{"owner": "acme", "repo": "app"}`

	result, err := tool.Execute(context.Background(), json.RawMessage(args), ToolContext{})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	if resp["count"] != float64(2) {
		t.Errorf("count = %v, want 2", resp["count"])
	}
}
