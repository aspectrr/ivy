// Package auth handles ClickUp OAuth2 authentication flows.
// It supports both personal API tokens and the OAuth2 authorization code flow
// with a localhost callback server.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"
)

const (
	// DefaultOAuthRedirectPort is the localhost port used for the OAuth callback.
	DefaultOAuthRedirectPort = 8765

	// ClickUpAuthURL is the ClickUp OAuth authorization endpoint.
	ClickUpAuthURL = "https://app.clickup.com/api"
)

// ClickUpTokenURL is the ClickUp OAuth token exchange endpoint.
// This is a var (not const) so tests can override it with a test server.
var ClickUpTokenURL = "https://api.clickup.com/api/v2/oauth/token"

// OAuthApp holds the credentials for the Ivy ClickUp OAuth app.
// These are hardcoded with sensible defaults (the official Ivy app)
// but can be overridden via environment variables for development.
type OAuthApp struct {
	ClientID     string
	ClientSecret string
}

// DefaultOAuthApp returns the Ivy OAuth app credentials.
//
// The defaults are the official Ivy ClickUp OAuth app. Override via
// environment variables for development or custom deployments.
//
// To set the defaults, replace the empty strings below with your
// client_id and client_secret from https://app.clickup.com/settings/apps
func DefaultOAuthApp() OAuthApp {
	return OAuthApp{
		ClientID:     envOr("IVY_CLICKUP_CLIENT_ID", "Q17QDZIXAWQHPL3WAHWHMTCWQPZRBB8R"),                                     // TODO: set default client_id
		ClientSecret: envOr("IVY_CLICKUP_CLIENT_SECRET", "JWR2YMMSHJ6GPRSKY2IAGKRJH6WN8K90LLAPSN7D987GPIWMFOXOZAZ0N9947B0J"), // TODO: set default client_secret
	}
}

// TokenResult holds the result of a successful OAuth token exchange.
type TokenResult struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

// OAuthFlow performs the full OAuth2 authorization code flow:
//  1. Starts a localhost HTTP server to receive the callback
//  2. Opens the user's browser to ClickUp's authorization page
//  3. Waits for the callback with the authorization code
//  4. Exchanges the code for an access token
func OAuthFlow(ctx context.Context, app OAuthApp, port int) (*TokenResult, error) {
	if app.ClientID == "" {
		return nil, fmt.Errorf("missing IVY_CLICKUP_CLIENT_ID — set it or configure the OAuth app credentials")
	}
	if app.ClientSecret == "" {
		return nil, fmt.Errorf("missing IVY_CLICKUP_CLIENT_SECRET — set it or configure the OAuth app credentials")
	}

	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	// Start the localhost callback server.
	resultCh := make(chan *TokenResult, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Success page shown in the browser after auth completes.
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		errorMsg := r.URL.Query().Get("error")

		if errorMsg != "" {
			desc := r.URL.Query().Get("error_description")
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, callbackErrorPage, errorMsg, desc)
			errCh <- fmt.Errorf("clickup authorization denied: %s: %s", errorMsg, desc)
			return
		}

		if code == "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, callbackErrorPage, "missing_code", "No authorization code was returned")
			errCh <- fmt.Errorf("no authorization code in callback")
			return
		}

		// Exchange code for token.
		token, err := exchangeCode(ctx, app, code, redirectURI)
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprintf(w, callbackErrorPage, "token_exchange_failed", err.Error())
			errCh <- fmt.Errorf("exchanging code for token: %w", err)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, callbackSuccessPage, token.AccessToken)
		resultCh <- token
	})

	// Find an available port if the default is taken.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		// Try a random port.
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("starting localhost callback server: %w", err)
		}
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	if actualPort != port {
		redirectURI = fmt.Sprintf("http://localhost:%d/callback", actualPort)
	}

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server error: %w", err)
		}
	}()

	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// Build the authorization URL.
	authURL := buildAuthURL(app.ClientID, redirectURI)

	// Open the browser.
	fmt.Fprintf(os.Stderr, "\nOpening browser to connect your ClickUp workspace...\n")
	fmt.Fprintf(os.Stderr, "If the browser doesn't open, visit:\n\n  %s\n\n", authURL)

	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser automatically. Please visit the URL above.\n")
	}

	// Wait for the callback, token exchange, or interruption.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	select {
	case result := <-resultCh:
		return result, nil
	case err := <-errCh:
		return nil, err
	case <-sigCh:
		return nil, fmt.Errorf("interrupted by user")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// exchangeCode exchanges an OAuth authorization code for an access token.
func exchangeCode(ctx context.Context, app OAuthApp, code, redirectURI string) (*TokenResult, error) {
	data := url.Values{}
	data.Set("client_id", app.ClientID)
	data.Set("client_secret", app.ClientSecret)
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ClickUpTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var token TokenResult
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}

	if token.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}

	return &token, nil
}

// buildAuthURL constructs the ClickUp OAuth authorization URL.
func buildAuthURL(clientID, redirectURI string) string {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	return ClickUpAuthURL + "?" + params.Encode()
}

// openBrowser tries to open the given URL in the user's default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux, freebsd, etc.
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// ValidateToken makes a simple API call to verify a token is valid.
func ValidateToken(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.clickup.com/api/v2/user", nil)
	if err != nil {
		return fmt.Errorf("creating validation request: %w", err)
	}
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("validating token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("token is invalid or expired")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d during validation", resp.StatusCode)
	}

	return nil
}

// GetAuthorizedTeams fetches the workspaces authorized for the given token.
// Useful after OAuth to show which workspaces were connected.
func GetAuthorizedTeams(ctx context.Context, token string) ([]AuthorizedTeam, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.clickup.com/api/v2/team", nil)
	if err != nil {
		return nil, fmt.Errorf("creating teams request: %w", err)
	}
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching teams: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading teams response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("teams request failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Teams []AuthorizedTeam `json:"teams"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding teams response: %w", err)
	}

	return result.Teams, nil
}

// AuthorizedTeam represents a ClickUp workspace/team.
type AuthorizedTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Space represents a ClickUp space within a team.
type Space struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// List represents a ClickUp list within a space.
type List struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GetSpaces fetches all spaces in a team.
func GetSpaces(ctx context.Context, token, teamID string) ([]Space, error) {
	url := fmt.Sprintf("https://api.clickup.com/api/v2/team/%s/space", teamID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating spaces request: %w", err)
	}
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching spaces: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading spaces response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("spaces request failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Spaces []Space `json:"spaces"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding spaces response: %w", err)
	}

	return result.Spaces, nil
}

// GetLists fetches all lists in a space.
func GetLists(ctx context.Context, token, spaceID string) ([]List, error) {
	url := fmt.Sprintf("https://api.clickup.com/api/v2/space/%s/list", spaceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating lists request: %w", err)
	}
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching lists: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading lists response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lists request failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Lists []List `json:"lists"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding lists response: %w", err)
	}

	return result.Lists, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Suppress unused import warning for log — used in tests.
var _ = log.Printf

// callbackSuccessPage is an HTML template displayed after successful OAuth.
// %s is replaced with the access token.
const callbackSuccessPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Ivy \u2014 Connected</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    min-height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #0a0f0d;
    color: #e0e8e4;
    overflow: hidden;
    position: relative;
  }

  /* \u2500\u2500 Diagonal pulsing wave blocks \u2500\u2500 */
  .waves {
    position: fixed;
    inset: 0;
    z-index: 0;
    pointer-events: none;
    overflow: hidden;
  }
  .wave-row {
    position: absolute;
    display: flex;
    gap: 6px;
    animation: drift 8s linear infinite;
    opacity: 0;
  }
  .wave-row:nth-child(1)  { top: 10%%; animation-delay: 0s;    --speed: 8s; }
  .wave-row:nth-child(2)  { top: 20%%; animation-delay: -1s;   --speed: 7s; }
  .wave-row:nth-child(3)  { top: 30%%; animation-delay: -2s;   --speed: 9s; }
  .wave-row:nth-child(4)  { top: 40%%; animation-delay: -0.5s; --speed: 6s; }
  .wave-row:nth-child(5)  { top: 50%%; animation-delay: -3s;   --speed: 8s; }
  .wave-row:nth-child(6)  { top: 60%%; animation-delay: -1.5s; --speed: 7s; }
  .wave-row:nth-child(7)  { top: 70%%; animation-delay: -2.5s; --speed: 9s; }
  .wave-row:nth-child(8)  { top: 80%%; animation-delay: -4s;   --speed: 6s; }
  .wave-row:nth-child(9)  { top: 90%%; animation-delay: -3.5s; --speed: 8s; }
  .wave-row:nth-child(10) { top: 100%%; animation-delay: -1.2s; --speed: 7s; }
  @keyframes drift {
    0%%   { transform: translate(110vw, 20vh); opacity: 0; }
    10%%  { opacity: 1; }
    90%%  { opacity: 1; }
    100%% { transform: translate(-40vw, -20vh); opacity: 0; }
  }
  .wave-row { animation-duration: var(--speed); }
  .block {
    width: 12px;
    height: 12px;
    border-radius: 3px;
    background: #2d6a4f;
    animation: pulse 2s ease-in-out infinite;
  }
  .wave-row:nth-child(odd) .block  { animation-delay: calc(var(--i, 0) * 0.15s); }
  .wave-row:nth-child(even) .block { animation-delay: calc(var(--i, 0) * 0.15s + 0.5s); }
  @keyframes pulse {
    0%%, 100%% { opacity: 0.15; transform: scale(0.8); background: #2d6a4f; }
    50%%      { opacity: 0.6;  transform: scale(1.1); background: #52b788; }
  }

  /* \u2500\u2500 Content card \u2500\u2500 */
  .card {
    position: relative;
    z-index: 1;
    background: rgba(16, 24, 20, 0.85);
    backdrop-filter: blur(24px);
    border: 1px solid rgba(82, 183, 136, 0.2);
    border-radius: 16px;
    padding: 48px 44px;
    max-width: 560px;
    width: 90vw;
    text-align: center;
  }
  .logo { font-size: 42px; margin-bottom: 8px; }
  .brand {
    font-size: 28px;
    font-weight: 700;
    letter-spacing: -0.5px;
    color: #b7e4c7;
    margin-bottom: 4px;
  }
  .brand span { color: #52b788; }
  .subtitle {
    font-size: 14px;
    color: #6b8f7b;
    margin-bottom: 28px;
  }
  .divider {
    height: 1px;
    background: linear-gradient(90deg, transparent, #2d6a4f, transparent);
    margin: 24px 0;
  }

  /* \u2500\u2500 Steps \u2500\u2500 */
  .steps {
    text-align: left;
    margin: 20px 0;
  }
  .step {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 10px 0;
    font-size: 14px;
    line-height: 1.5;
    color: #a7c4b2;
  }
  .step-num {
    flex-shrink: 0;
    width: 24px;
    height: 24px;
    border-radius: 50%%;
    background: #1b4332;
    color: #52b788;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 700;
  }

  /* \u2500\u2500 Code block \u2500\u2500 */
  .codeblock {
    position: relative;
    background: #0d1410;
    border: 1px solid rgba(82, 183, 136, 0.15);
    border-radius: 10px;
    padding: 16px 18px;
    text-align: left;
    margin: 16px 0;
  }
  .codeblock pre {
    font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
    font-size: 12.5px;
    line-height: 1.7;
    color: #95d5b2;
    white-space: pre;
    overflow-x: auto;
    margin: 0;
  }
  .codeblock .comment { color: #4a7a5e; }
  .codeblock .key     { color: #74c69d; }
  .codeblock .val     { color: #d8f3dc; }
  .copy-btn {
    position: absolute;
    top: 10px;
    right: 10px;
    background: rgba(45, 106, 79, 0.3);
    border: 1px solid rgba(82, 183, 136, 0.3);
    color: #95d5b2;
    border-radius: 6px;
    padding: 4px 10px;
    font-size: 11px;
    cursor: pointer;
    transition: all 0.2s;
  }
  .copy-btn:hover { background: rgba(45, 106, 79, 0.6); }
  .copy-btn:active { transform: scale(0.95); }

  /* \u2500\u2500 Mention tip \u2500\u2500 */
  .tip {
    background: rgba(45, 106, 79, 0.15);
    border: 1px solid rgba(82, 183, 136, 0.2);
    border-radius: 10px;
    padding: 14px 18px;
    text-align: left;
    margin: 16px 0;
    font-size: 13.5px;
    line-height: 1.6;
    color: #a7c4b2;
  }
  .tip strong { color: #b7e4c7; }
  .tip code {
    background: rgba(45, 106, 79, 0.3);
    padding: 2px 6px;
    border-radius: 4px;
    font-family: 'SF Mono', 'Fira Code', monospace;
    font-size: 12px;
    color: #d8f3dc;
  }

  /* \u2500\u2500 Footer \u2500\u2500 */
  .footer {
    margin-top: 24px;
    font-size: 13px;
    color: #4a7a5e;
  }
</style>
</head>
<body>

<!-- Diagonal wave blocks -->
<div class="waves">
  <div class="wave-row">
    <div class="block" style="--i:0"></div><div class="block" style="--i:1"></div>
    <div class="block" style="--i:2"></div><div class="block" style="--i:3"></div>
    <div class="block" style="--i:4"></div><div class="block" style="--i:5"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:6"></div><div class="block" style="--i:7"></div>
    <div class="block" style="--i:8"></div><div class="block" style="--i:9"></div>
    <div class="block" style="--i:10"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:0"></div><div class="block" style="--i:1"></div>
    <div class="block" style="--i:2"></div><div class="block" style="--i:3"></div>
    <div class="block" style="--i:4"></div><div class="block" style="--i:5"></div>
    <div class="block" style="--i:6"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:3"></div><div class="block" style="--i:4"></div>
    <div class="block" style="--i:5"></div><div class="block" style="--i:6"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:7"></div><div class="block" style="--i:8"></div>
    <div class="block" style="--i:9"></div><div class="block" style="--i:10"></div>
    <div class="block" style="--i:11"></div><div class="block" style="--i:12"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:2"></div><div class="block" style="--i:3"></div>
    <div class="block" style="--i:4"></div><div class="block" style="--i:5"></div>
    <div class="block" style="--i:6"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:5"></div><div class="block" style="--i:6"></div>
    <div class="block" style="--i:7"></div><div class="block" style="--i:8"></div>
    <div class="block" style="--i:9"></div><div class="block" style="--i:10"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:1"></div><div class="block" style="--i:2"></div>
    <div class="block" style="--i:3"></div><div class="block" style="--i:4"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:4"></div><div class="block" style="--i:5"></div>
    <div class="block" style="--i:6"></div><div class="block" style="--i:7"></div>
    <div class="block" style="--i:8"></div><div class="block" style="--i:9"></div>
    <div class="block" style="--i:10"></div>
  </div>
  <div class="wave-row">
    <div class="block" style="--i:0"></div><div class="block" style="--i:1"></div>
    <div class="block" style="--i:2"></div><div class="block" style="--i:3"></div>
    <div class="block" style="--i:4"></div>
  </div>
</div>

<!-- Content -->
<div class="card">
  <div class="logo">\xf0\x9f\x8c\xbf</div>
  <div class="brand">\xf0\x9f\x8c\xbf <span>Ivy</span></div>
  <div class="subtitle">ClickUp workspace connected</div>

  <div class="divider"></div>

  <div class="steps">
    <div class="step">
      <div class="step-num">1</div>
      <div>Return to your terminal to finish workspace, space, and list selection.</div>
    </div>
    <div class="step">
      <div class="step-num">2</div>
      <div>Add the generated <code style="background:rgba(45,106,79,0.3);padding:2px 6px;border-radius:4px;font-family:'SF Mono','Fira Code',monospace;font-size:12px;color:#d8f3dc">.env</code> vars to your vine config.</div>
    </div>
    <div class="step">
      <div class="step-num">3</div>
      <div>Set <code style="background:rgba(45,106,79,0.3);padding:2px 6px;border-radius:4px;font-family:'SF Mono','Fira Code',monospace;font-size:12px;color:#d8f3dc">agent_username</code> so the bot responds to @mentions.</div>
    </div>
  </div>

  <div class="codeblock">
    <button class="copy-btn" onclick="copyEnv()">Copy</button>
    <pre id="env-block"><span class="comment"># .env \u2014 paste into your vine config or .env file</span>
<span class="key">IVY_CLICKUP_API_TOKEN</span>=<span class="val">"%s"</span>
<span class="key">IVY_CLICKUP_TEAM_ID</span>=<span class="val">"&lt;from terminal&gt;"</span>
<span class="key">IVY_CLICKUP_AUTH_MODE</span>=<span class="val">"oauth"</span>
<span class="key">IVY_CLICKUP_SPACE_ID</span>=<span class="val">"&lt;from terminal&gt;"</span>
<span class="key">IVY_CLICKUP_LIST_ID</span>=<span class="val">"&lt;from terminal&gt;"</span>
</pre>
  </div>

  <div class="tip">
    <strong>\xf0\x9f\x92\xac Triggering the agent:</strong> In any ClickUp task comment, type
    <code>@IvyAgent</code> to summon Ivy (the default username). You can also assign tasks
    directly to the agent user and it will pick them up automatically.
  </div>

  <div class="footer">This tab can be closed safely.</div>
</div>

<script>
function copyEnv() {
  const block = document.getElementById('env-block');
  const text = block.innerText;
  navigator.clipboard.writeText(text).then(() => {
    const btn = document.querySelector('.copy-btn');
    btn.textContent = '\u2713 Copied!';
    setTimeout(() => { btn.textContent = 'Copy'; }, 2000);
  });
}
</script>
</body>
</html>`

const callbackErrorPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Ivy — Error</title>
</head>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#0a0f0d;color:#e0e8e4">
<div style="text-align:center;max-width:480px;padding:40px">
<h1 style="color:#e07070;font-size:24px;margin-bottom:16px">✗ Authorization Failed</h1>
<p style="margin-bottom:8px"><b>%s</b>: %s</p>
<p style="color:#6b8f7b;margin-top:20px">Please return to your terminal and try again.</p>
</div>
</body>
</html>`
