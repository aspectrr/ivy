// ivy is the CLI companion to the vine daemon.
// It handles authentication flows so operators can connect external services
// (like ClickUp) without needing a publicly accessible vine host.
//
// Install:
//
//	go install github.com/aspectrr/ivy/cmd/ivy@latest
//
// Usage:
//
//	ivy auth clickup              # OAuth flow (opens browser)
//	ivy auth clickup --personal   # Show instructions for a personal API token
//	ivy auth clickup --validate   # Validate a token from env or stdin
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aspectrr/ivy/internal/vine/connector/clickup/auth"
)

const (
	exitOK   = 0
	exitErr  = 1
	exitAuth = 2
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitErr)
	}

	switch os.Args[1] {
	case "auth":
		authCmd(os.Args[2:])
	case "version":
		fmt.Println("ivy CLI v0.1.0")
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(exitErr)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ivy — CLI companion for Ivy vine daemon

Usage:
  ivy <command> [subcommand] [flags]

Commands:
  auth clickup              Connect a ClickUp workspace via OAuth
  auth clickup --personal   Connect using a personal API token
  auth clickup --validate   Validate an existing token
  version                   Print version
  help                      Show this help

Authentication:
  The OAuth flow runs entirely on your laptop. No inbound access to
  the vine host is required. After connecting, you'll receive a token
  to add to your vine configuration.

  For enterprise setups, use OAuth so no extra ClickUp seat is needed.
  Any workspace member can authorize — admin is not required, but someone
  with broad workspace access is recommended so the agent can see all tasks.

Environment Variables:
  IVY_CLICKUP_CLIENT_ID       OAuth client ID (defaults to Ivy's public app)
  IVY_CLICKUP_CLIENT_SECRET   OAuth client secret (defaults to Ivy's public app)

Examples:
  ivy auth clickup                        # OAuth — opens browser
  ivy auth clickup --personal             # Shows personal token instructions
  IVY_CLICKUP_API_TOKEN=pk_xxx ivy auth clickup --validate  # Validate a token
`)
}

func authCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, "usage: ivy auth <service>\n\nServices: clickup\n")
		os.Exit(exitErr)
	}

	switch args[0] {
	case "clickup":
		authClickUp(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown auth service: %s\n\nServices: clickup\n", args[0])
		os.Exit(exitErr)
	}
}

func authClickUp(args []string) {
	fs := flag.NewFlagSet("auth clickup", flag.ExitOnError)
	personal := fs.Bool("personal", false, "use a personal API token instead of OAuth")
	validate := fs.Bool("validate", false, "validate an existing token (reads from IVY_CLICKUP_API_TOKEN env or stdin)")
	port := fs.Int("port", auth.DefaultOAuthRedirectPort, "localhost port for OAuth callback")
	fs.Parse(args)

	if *validate {
		validateClickUpToken()
		return
	}

	if *personal {
		personalTokenInstructions()
		return
	}

	oauthFlow(*port)
}

func oauthFlow(port int) {
	app := auth.DefaultOAuthApp()

	if app.ClientID == "" {
		fmt.Fprint(os.Stderr, `
⚠  OAuth credentials not configured.

Before using the OAuth flow, you need to create a ClickUp OAuth app:

  1. Go to https://app.clickup.com/settings/apps
  2. Click "Create new app"
  3. Name it "Ivy" (or whatever you prefer)
  4. Set the redirect URL to: http://localhost:8765/callback
  5. Copy the Client ID and Client Secret

Then run:

  export IVY_CLICKUP_CLIENT_ID="your_client_id"
  export IVY_CLICKUP_CLIENT_SECRET="your_client_secret"
  ivy auth clickup

For a quicker setup, use a personal token instead:

  ivy auth clickup --personal

`)
		os.Exit(exitAuth)
	}

	fmt.Fprintf(os.Stderr, "Starting ClickUp OAuth flow...\n\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := auth.OAuthFlow(ctx, app, port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n✗ OAuth flow failed: %s\n", err)
		os.Exit(exitErr)
	}

	// Try to fetch authorized teams to give the user more context.
	teams, teamsErr := auth.GetAuthorizedTeams(ctx, result.AccessToken)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "✓ ClickUp workspace connected!")
	if teamsErr == nil && len(teams) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprint(os.Stderr, "Authorized workspaces:\n")
		for _, t := range teams {
			fmt.Fprintf(os.Stderr, "  • %s (ID: %s)\n", t.Name, t.ID)
		}
	}

	// Print the token and config instructions.
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "── Token ──────────────────────────────────────")
	fmt.Fprintf(os.Stderr, "%s\n", result.AccessToken)
	fmt.Fprintln(os.Stderr, "───────────────────────────────────────────────")
	fmt.Fprintln(os.Stderr)
	fmt.Fprint(os.Stderr, `Add to your vine configuration:

  connectors:
    clickup:
      enabled: true
      auth_mode: oauth
      api_token: <paste-token-above>
      team_id: <your-team-id>

Or set environment variables:

  export IVY_CLICKUP_API_TOKEN="<paste-token-above>"
  export IVY_CLICKUP_TEAM_ID="<your-team-id>"
  export IVY_CLICKUP_AUTH_MODE="oauth"

`)
}

func personalTokenInstructions() {
	fmt.Fprint(os.Stderr, `
── Personal API Token ──────────────────────────────────────────

A personal API token is tied to a single ClickUp user. For enterprise
workspaces, prefer the OAuth flow (ivy auth clickup) to avoid using
an extra seat.

To generate a personal token:

  1. Log in to ClickUp
  2. Click your avatar (top right) → Settings
  3. Click "Apps" in the sidebar
  4. Under "API Token", click "Generate"
  5. Copy the token (starts with pk_)

Then add to your vine configuration:

  connectors:
    clickup:
      enabled: true
      auth_mode: personal
      api_token: pk_your_token_here
      team_id: <your-team-id>

Or set environment variables:

  export IVY_CLICKUP_API_TOKEN="pk_your_token_here"
  export IVY_CLICKUP_TEAM_ID="<your-team-id>"
  export IVY_CLICKUP_AUTH_MODE="personal"

To validate your token:

  IVY_CLICKUP_API_TOKEN=pk_xxx ivy auth clickup --validate

`)
}

func validateClickUpToken() {
	token := os.Getenv("IVY_CLICKUP_API_TOKEN")
	if token == "" {
		// Try reading from stdin.
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			buf := make([]byte, 4096)
			n, _ := os.Stdin.Read(buf)
			token = strings.TrimSpace(string(buf[:n]))
		}
	}

	if token == "" {
		fmt.Fprint(os.Stderr, "No token provided. Set IVY_CLICKUP_API_TOKEN or pipe a token to stdin.\n")
		os.Exit(exitErr)
	}

	fmt.Fprintf(os.Stderr, "Validating token...\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := auth.ValidateToken(ctx, token); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Token validation failed: %s\n", err)
		os.Exit(exitErr)
	}

	fmt.Fprintln(os.Stderr, "✓ Token is valid!")

	// Show authorized teams.
	teams, err := auth.GetAuthorizedTeams(ctx, token)
	if err == nil && len(teams) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprint(os.Stderr, "Accessible workspaces:\n")
		for _, t := range teams {
			fmt.Fprintf(os.Stderr, "  • %s (ID: %s)\n", t.Name, t.ID)
		}
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Token type: %s\n", tokenType(token))
}

// tokenType guesses the auth mode from the token prefix.
func tokenType(token string) string {
	if strings.HasPrefix(token, "pk_") {
		return "personal (pk_ prefix)"
	}
	return "oauth (bearer token)"
}
