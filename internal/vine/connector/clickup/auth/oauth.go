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
		ClientID:     envOr("IVY_CLICKUP_CLIENT_ID", ""),  // TODO: set default client_id
		ClientSecret: envOr("IVY_CLICKUP_CLIENT_SECRET", ""), // TODO: set default client_secret
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

		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, callbackSuccessPage)
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

// HTML pages shown in the browser after the OAuth callback.
const callbackSuccessPage = `<!DOCTYPE html>
<html>
<head><title>Ivy — Connected</title></head>
<body style="font-family:-apple-system,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#f5f5f5">
<div style="text-align:center">
<h1 style="color:#4a9f4a">✓ Connected!</h1>
<p>Your ClickUp workspace is now linked to Ivy.</p>
<p>You can close this tab and return to your terminal.</p>
</div>
</body>
</html>`

const callbackErrorPage = `<!DOCTYPE html>
<html>
<head><title>Ivy — Error</title></head>
<body style="font-family:-apple-system,sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#f5f5f5">
<div style="text-align:center">
<h1 style="color:#c44">✗ Authorization Failed</h1>
<p><b>%s</b>: %s</p>
<p>Please return to your terminal and try again.</p>
</div>
</body>
</html>`
