package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultBaseURL    = "https://api.github.com"
	tokenRefreshAhead = 5 * time.Minute
)

// InstallationToken represents a GitHub App installation access token.
type InstallationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// InstallationsResponse is the API response for listing installations.
type InstallationsResponse struct {
	Installations []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	} `json:"installations"`
}

// TokenResponse is the API response for creating an installation token.
type TokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// PullRequest represents a GitHub pull request.
type PullRequest struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	State   string   `json:"state"`
	Head    PRBranch `json:"head"`
	Base    PRBranch `json:"base"`
	HTMLURL string   `json:"html_url"`
	Draft   bool     `json:"draft"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
}

// PRBranch represents a branch ref in a PR.
type PRBranch struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

// PullRequestListResponse is the API response for listing PRs.
type PullRequestListResponse []PullRequest

// CreatePROptions are the options for creating a pull request.
type CreatePROptions struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Draft bool   `json:"draft"`
}

// Comment represents an issue/PR comment.
type Comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Client is a GitHub App client that authenticates as an installation.
// It handles JWT generation, installation token refresh, and provides
// methods for the agent tools to interact with GitHub.
type Client struct {
	appID      int64
	privateKey *rsa.PrivateKey
	baseURL    string
	httpClient *http.Client

	mu            sync.Mutex
	installations map[int64]*InstallationToken // installationID → token
}

// Config holds the configuration for the GitHub App client.
type Config struct {
	AppID      int64
	PrivateKey string // PEM-encoded RSA private key
	BaseURL    string // defaults to https://api.github.com
}

// NewClient creates a new GitHub App client from the given config.
func NewClient(cfg Config) (*Client, error) {
	if cfg.AppID == 0 {
		return nil, fmt.Errorf("github: app_id is required")
	}

	key, err := parsePrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("github: parsing private key: %w", err)
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &Client{
		appID:         cfg.AppID,
		privateKey:    key,
		baseURL:       baseURL,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		installations: make(map[int64]*InstallationToken),
	}, nil
}

// AppID returns the configured app ID.
func (c *Client) AppID() int64 {
	return c.appID
}

// BaseURL returns the configured API base URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// --- Authentication ---

// generateJWT creates a signed JSON Web Token for the GitHub App.
// The JWT is valid for 10 minutes (GitHub max) and is used to authenticate
// as the app itself (not as an installation).
func (c *Client) generateJWT() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": c.appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(c.privateKey)
}

// getInstallationToken returns a valid installation access token, refreshing
// if necessary. Tokens are cached and refreshed 5 minutes before expiry.
func (c *Client) getInstallationToken(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	tok, exists := c.installations[installationID]
	c.mu.Unlock()

	if exists && time.Now().Add(tokenRefreshAhead).Before(tok.ExpiresAt) {
		return tok.Token, nil
	}

	// Generate a new token.
	jwtStr, err := c.generateJWT()
	if err != nil {
		return "", fmt.Errorf("generating JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub returned %d creating installation token: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, tokenResp.ExpiresAt)
	if err != nil {
		return "", fmt.Errorf("parsing token expiry: %w", err)
	}

	newTok := &InstallationToken{
		Token:     tokenResp.Token,
		ExpiresAt: expiresAt,
	}

	c.mu.Lock()
	c.installations[installationID] = newTok
	c.mu.Unlock()

	return newTok.Token, nil
}

// --- Installation Discovery ---

// ListInstallations returns all installations of the GitHub App.
func (c *Client) ListInstallations(ctx context.Context) ([]struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}, error) {
	jwtStr, err := c.generateJWT()
	if err != nil {
		return nil, fmt.Errorf("generating JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing installations: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub returned %d listing installations: %s", resp.StatusCode, string(body))
	}

	var result InstallationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding installations: %w", err)
	}

	return result.Installations, nil
}

// GetInstallationForRepo finds the installation ID for a specific repository.
func (c *Client) GetInstallationForRepo(ctx context.Context, owner, repo string) (int64, error) {
	jwtStr, err := c.generateJWT()
	if err != nil {
		return 0, fmt.Errorf("generating JWT: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/installation", c.baseURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching installation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("GitHub returned %d looking up installation for %s/%s: %s", resp.StatusCode, owner, repo, string(body))
	}

	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decoding installation: %w", err)
	}

	return result.ID, nil
}

// --- Authenticated API Methods ---

// CreateRef creates a new branch ref pointing at the given SHA.
// This is a convenience wrapper around CreateOrUpdateRef for the GitHubClient interface.
func (c *Client) CreateRef(ctx context.Context, installationID int64, owner, repo, ref, sha string) error {
	return c.CreateOrUpdateRef(ctx, installationID, owner, repo, ref, sha)
}

// doAuthenticated performs an API request as the app installation for the given repo.
func (c *Client) doAuthenticated(ctx context.Context, installationID int64, method, path string, body io.Reader) (*http.Response, error) {
	token, err := c.getInstallationToken(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("getting installation token: %w", err)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	return c.httpClient.Do(req)
}

// --- Git Data API (for creating blobs, trees, commits, refs) ---

// CreateOrUpdateRef creates or updates a Git reference (branch).
func (c *Client) CreateOrUpdateRef(ctx context.Context, installationID int64, owner, repo, ref, sha string) error {
	// First, try to get the ref to see if it already exists.
	path := fmt.Sprintf("/repos/%s/%s/git/refs/heads/%s", owner, repo, ref)
	resp, err := c.doAuthenticated(ctx, installationID, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("checking ref: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Ref exists, update it.
		body := fmt.Sprintf(`{"sha":"%s","force":false}`, sha)
		resp, err := c.doAuthenticated(ctx, installationID, http.MethodPatch, path, strings.NewReader(body))
		if err != nil {
			return fmt.Errorf("updating ref: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("GitHub returned %d updating ref: %s", resp.StatusCode, string(respBody))
		}
		return nil
	}

	// Ref doesn't exist, create it.
	body := fmt.Sprintf(`{"ref":"refs/heads/%s","sha":"%s"}`, ref, sha)
	resp, err = c.doAuthenticated(ctx, installationID, http.MethodPost, fmt.Sprintf("/repos/%s/%s/git/refs", owner, repo), strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating ref: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub returned %d creating ref: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// CreateCommit creates a commit on a branch using the Git Data API.
// It creates a blob, tree, and commit, then updates the branch ref.
func (c *Client) CreateCommit(ctx context.Context, installationID int64, owner, repo, branch, message string, files map[string]string) (string, error) {
	// Step 1: Get the current branch tip.
	refPath := fmt.Sprintf("/repos/%s/%s/git/refs/heads/%s", owner, repo, branch)
	resp, err := c.doAuthenticated(ctx, installationID, http.MethodGet, refPath, nil)
	if err != nil {
		return "", fmt.Errorf("getting branch ref: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub returned %d getting branch ref: %s", resp.StatusCode, string(respBody))
	}

	var refData struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refData); err != nil {
		return "", fmt.Errorf("decoding ref: %w", err)
	}
	parentSHA := refData.Object.SHA

	// Step 2: Get the current commit to find its tree.
	commitPath := fmt.Sprintf("/repos/%s/%s/git/commits/%s", owner, repo, parentSHA)
	resp, err = c.doAuthenticated(ctx, installationID, http.MethodGet, commitPath, nil)
	if err != nil {
		return "", fmt.Errorf("getting parent commit: %w", err)
	}
	defer resp.Body.Close()

	var commitData struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commitData); err != nil {
		return "", fmt.Errorf("decoding commit: %w", err)
	}

	// Step 3: Create blobs for each file and build tree entries.
	var treeEntries []map[string]interface{}
	for path, content := range files {
		// Create blob.
		blobBody := fmt.Sprintf(`{"content":%s,"encoding":"utf-8"}`, jsonString(content))
		resp, err := c.doAuthenticated(ctx, installationID, http.MethodPost,
			fmt.Sprintf("/repos/%s/%s/git/blobs", owner, repo),
			strings.NewReader(blobBody))
		if err != nil {
			return "", fmt.Errorf("creating blob for %s: %w", path, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			respBody, _ := io.ReadAll(resp.Body)
			return "", fmt.Errorf("GitHub returned %d creating blob: %s", resp.StatusCode, string(respBody))
		}

		var blobResult struct {
			SHA string `json:"sha"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&blobResult); err != nil {
			return "", fmt.Errorf("decoding blob: %w", err)
		}

		treeEntries = append(treeEntries, map[string]interface{}{
			"path": path,
			"mode": "100644",
			"type": "blob",
			"sha":  blobResult.SHA,
		})
	}

	// Step 4: Create a new tree.
	treeReq := map[string]interface{}{
		"base_tree": commitData.Tree.SHA,
		"tree":      treeEntries,
	}
	treeBody, err := json.Marshal(treeReq)
	if err != nil {
		return "", fmt.Errorf("marshaling tree request: %w", err)
	}

	resp, err = c.doAuthenticated(ctx, installationID, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/git/trees", owner, repo),
		strings.NewReader(string(treeBody)))
	if err != nil {
		return "", fmt.Errorf("creating tree: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub returned %d creating tree: %s", resp.StatusCode, string(respBody))
	}

	var treeResult struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&treeResult); err != nil {
		return "", fmt.Errorf("decoding tree: %w", err)
	}

	// Step 5: Create the commit.
	commitReq := map[string]interface{}{
		"message": message,
		"tree":    treeResult.SHA,
		"parents": []string{parentSHA},
	}
	commitBody, err := json.Marshal(commitReq)
	if err != nil {
		return "", fmt.Errorf("marshaling commit request: %w", err)
	}

	resp, err = c.doAuthenticated(ctx, installationID, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/git/commits", owner, repo),
		strings.NewReader(string(commitBody)))
	if err != nil {
		return "", fmt.Errorf("creating commit: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub returned %d creating commit: %s", resp.StatusCode, string(respBody))
	}

	var commitResult struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commitResult); err != nil {
		return "", fmt.Errorf("decoding commit: %w", err)
	}

	// Step 6: Update the branch ref to point to the new commit.
	if err := c.CreateOrUpdateRef(ctx, installationID, owner, repo, branch, commitResult.SHA); err != nil {
		return "", fmt.Errorf("updating branch ref: %w", err)
	}

	return commitResult.SHA, nil
}

// --- Pull Request API ---

// ListPullRequests lists PRs for a repo, optionally filtered.
func (c *Client) ListPullRequests(ctx context.Context, installationID int64, owner, repo, state, head string) (PullRequestListResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?per_page=50", owner, repo)
	if state != "" {
		path += "&state=" + state
	}
	if head != "" {
		path += "&head=" + head
	}

	resp, err := c.doAuthenticated(ctx, installationID, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub returned %d listing PRs: %s", resp.StatusCode, string(body))
	}

	var prs PullRequestListResponse
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return nil, fmt.Errorf("decoding PRs: %w", err)
	}

	return prs, nil
}

// GetPullRequest gets a single PR by number.
func (c *Client) GetPullRequest(ctx context.Context, installationID int64, owner, repo string, number int) (*PullRequest, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)

	resp, err := c.doAuthenticated(ctx, installationID, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("getting PR: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub returned %d getting PR: %s", resp.StatusCode, string(body))
	}

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding PR: %w", err)
	}

	return &pr, nil
}

// CreatePullRequest creates a new pull request.
func (c *Client) CreatePullRequest(ctx context.Context, installationID int64, owner, repo string, opts CreatePROptions) (*PullRequest, error) {
	body, err := json.Marshal(opts)
	if err != nil {
		return nil, fmt.Errorf("marshaling PR request: %w", err)
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls", owner, repo)
	resp, err := c.doAuthenticated(ctx, installationID, http.MethodPost, path, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("creating PR: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub returned %d creating PR: %s", resp.StatusCode, string(respBody))
	}

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding PR: %w", err)
	}

	return &pr, nil
}

// UpdatePullRequest updates an existing pull request (title, body).
func (c *Client) UpdatePullRequest(ctx context.Context, installationID int64, owner, repo string, number int, title, body string) (*PullRequest, error) {
	updates := map[string]string{}
	if title != "" {
		updates["title"] = title
	}
	if body != "" {
		updates["body"] = body
	}

	reqBody, err := json.Marshal(updates)
	if err != nil {
		return nil, fmt.Errorf("marshaling update: %w", err)
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	resp, err := c.doAuthenticated(ctx, installationID, http.MethodPatch, path, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("updating PR: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub returned %d updating PR: %s", resp.StatusCode, string(respBody))
	}

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding PR: %w", err)
	}

	return &pr, nil
}

// --- Comments API (issues API, works for PRs too) ---

// CommentOnPullRequest adds a comment to a PR (uses the issues comments API).
func (c *Client) CommentOnPullRequest(ctx context.Context, installationID int64, owner, repo string, pullNumber int, body string) (*Comment, error) {
	reqBody := map[string]string{"body": body}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling comment: %w", err)
	}

	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, pullNumber)
	resp, err := c.doAuthenticated(ctx, installationID, http.MethodPost, path, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("creating comment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub returned %d creating comment: %s", resp.StatusCode, string(respBody))
	}

	var comment Comment
	if err := json.NewDecoder(resp.Body).Decode(&comment); err != nil {
		return nil, fmt.Errorf("decoding comment: %w", err)
	}

	return &comment, nil
}

// --- File Contents API ---

// GetFileContents reads a file from the repository.
func (c *Client) GetFileContents(ctx context.Context, installationID int64, owner, repo, path, ref string) (string, error) {
	apiPath := fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path)
	if ref != "" {
		apiPath += "?ref=" + ref
	}

	resp, err := c.doAuthenticated(ctx, installationID, http.MethodGet, apiPath, nil)
	if err != nil {
		return "", fmt.Errorf("getting file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub returned %d getting file: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		SHA      string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding file: %w", err)
	}

	return result.SHA, nil
}

// --- Repository API ---

// GetBranchSHA returns the SHA of the latest commit on a branch.
func (c *Client) GetBranchSHA(ctx context.Context, installationID int64, owner, repo, branch string) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/git/refs/heads/%s", owner, repo, branch)
	resp, err := c.doAuthenticated(ctx, installationID, http.MethodGet, path, nil)
	if err != nil {
		return "", fmt.Errorf("getting branch ref: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GitHub returned %d getting branch SHA: %s", resp.StatusCode, string(body))
	}

	var refData struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refData); err != nil {
		return "", fmt.Errorf("decoding ref: %w", err)
	}

	return refData.Object.SHA, nil
}

// --- Helper ---

// jsonString returns a JSON-escaped string (without quotes).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// parsePrivateKey parses a PEM-encoded RSA private key.
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 as a fallback.
		keyIfc, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parsing as PKCS1 (%w) and PKCS8 (%w)", err, err2)
		}
		key, ok := keyIfc.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		return key, nil
	}

	return key, nil
}
