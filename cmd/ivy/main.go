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
//	ivy auth clickup              # OAuth flow (opens browser, guides team/space/list selection)
//	ivy auth clickup --personal   # Show instructions for a personal API token
//	ivy auth clickup --validate   # Validate a token from env or stdin
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aspectrr/ivy/internal/vine/connector/clickup/auth"
)

const (
	exitOK   = 0
	exitErr  = 1
	exitAuth = 2
)

var stdin *bufio.Reader

func init() {
	stdin = bufio.NewReader(os.Stdin)
}

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
  the vine host is required. After connecting, you'll pick which team,
  space, and list the agent is allowed to respond to @mentions in — so you
can scope it to a single board rather than your entire workspace.

  For enterprise setups, use OAuth so no extra ClickUp seat is needed.
  Any workspace member can authorize — admin is not required, but someone
  with broad workspace access is recommended so the agent can see all tasks.

Environment Variables:
  IVY_CLICKUP_CLIENT_ID       Override the built-in OAuth client ID
  IVY_CLICKUP_CLIENT_SECRET   Override the built-in OAuth client secret

Examples:
  ivy auth clickup                        # OAuth — opens browser, guided setup
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

// pickResult holds the user's selections from the guided setup.
type pickResult struct {
	Token     string
	TeamID    string
	TeamName  string
	SpaceID   string
	SpaceName string
	ListID    string
	ListName  string
}

func oauthFlow(port int) {
	app := auth.DefaultOAuthApp()

	if app.ClientID == "" {
		fmt.Fprint(os.Stderr, `
⚠  This is a development build — OAuth credentials are not configured.

To set up the OAuth app for development:

  1. Go to https://app.clickup.com/settings/apps
  2. Click "Create new app"
  3. Name it "Ivy Dev" (or whatever you prefer)
  4. Set the redirect URL to: http://localhost:8765/callback
  5. Copy the Client ID and Client Secret
  6. Set them before running:

       export IVY_CLICKUP_CLIENT_ID="your_client_id"
       export IVY_CLICKUP_CLIENT_SECRET="your_client_secret"
       ivy auth clickup

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

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "✓ ClickUp connected!")

	// Guided setup: team → space → list selection.
	pick := pickResult{Token: result.AccessToken}

	// ── Pick team ─────────────────────────────────────────────
	teams, err := auth.GetAuthorizedTeams(ctx, result.AccessToken)
	if err != nil || len(teams) == 0 {
		fmt.Fprintf(os.Stderr, "\n⚠  Could not fetch teams: %s\n", err)
		printConfigNoPick(pick)
		return
	}

	if len(teams) == 1 {
		pick.TeamID = teams[0].ID
		pick.TeamName = teams[0].Name
		fmt.Fprintf(os.Stderr, "\nWorkspace: %s\n", pick.TeamName)
	} else {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Which workspace should the agent be available in?")
		for i, t := range teams {
			fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, t.Name)
		}
		idx := promptSelect("Select workspace", len(teams))
		pick.TeamID = teams[idx].ID
		pick.TeamName = teams[idx].Name
	}

	// ── Pick space ─────────────────────────────────────────────
	spaces, err := auth.GetSpaces(ctx, result.AccessToken, pick.TeamID)
	if err != nil || len(spaces) == 0 {
		fmt.Fprintf(os.Stderr, "\n⚠  Could not fetch spaces: %s\n", err)
		printConfigNoPick(pick)
		return
	}

	if len(spaces) == 1 {
		pick.SpaceID = spaces[0].ID
		pick.SpaceName = spaces[0].Name
		fmt.Fprintf(os.Stderr, "Space:     %s\n", pick.SpaceName)
	} else {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Where should the agent respond to @mentions?")
		for i, s := range spaces {
			fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, s.Name)
		}
		idx := promptSelect("Select space", len(spaces))
		pick.SpaceID = spaces[idx].ID
		pick.SpaceName = spaces[idx].Name
	}

	// ── Pick list (optional) ───────────────────────────────────
	lists, err := auth.GetLists(ctx, result.AccessToken, pick.SpaceID)
	if err != nil || len(lists) == 0 {
		fmt.Fprintf(os.Stderr, "\n⚠  Could not fetch lists: %s\n", err)
		printConfig(pick)
		return
	}

	if len(lists) == 1 {
		pick.ListID = lists[0].ID
		pick.ListName = lists[0].Name
		fmt.Fprintf(os.Stderr, "List:      %s\n", pick.ListName)
	} else {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Which list should the agent respond in? (or 'all' for the entire space)")
		for i, l := range lists {
			fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, l.Name)
		}
		fmt.Fprintf(os.Stderr, "  a) All lists in %s\n", pick.SpaceName)

		choice := promptString("Select list")
		if choice == "a" || choice == "all" {
			// No list filter — agent responds in any list in the space.
		} else {
			idx, err := strconv.Atoi(choice)
			if err != nil || idx < 1 || idx > len(lists) {
				fmt.Fprintf(os.Stderr, "Invalid choice. Defaulting to all lists.\n")
			} else {
				pick.ListID = lists[idx-1].ID
				pick.ListName = lists[idx-1].Name
				fmt.Fprintf(os.Stderr, "List:      %s\n", pick.ListName)
			}
		}
	}

	printConfig(pick)
}

// printConfig prints the complete vine config snippet with all selected IDs.
func printConfig(pick pickResult) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "── Configuration ─────────────────────────────")

	listLine := ""
	if pick.ListID != "" {
		listLine = fmt.Sprintf("\n      list_id: %s", pick.ListID)
	}

	fmt.Fprintf(os.Stderr, `
Add to your vine configuration:

  connectors:
    clickup:
      enabled: true
      auth_mode: oauth
      api_token: %s
      team_id: %s
      space_id: %s%s

Or set environment variables:

  export IVY_CLICKUP_API_TOKEN="%s"
  export IVY_CLICKUP_TEAM_ID="%s"
  export IVY_CLICKUP_AUTH_MODE="oauth"
  export IVY_CLICKUP_SPACE_ID="%s"
`,
		pick.Token, pick.TeamID, pick.SpaceID, listLine,
		pick.Token, pick.TeamID, pick.SpaceID,
	)

	if pick.ListID != "" {
		fmt.Fprintf(os.Stderr, `  export IVY_CLICKUP_LIST_ID="%s"
`, pick.ListID)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Agent will respond to @mentions in: %s / %s", pick.TeamName, pick.SpaceName)
	if pick.ListName != "" {
		fmt.Fprintf(os.Stderr, " / %s", pick.ListName)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr)
}

// printConfigNoPick prints config when team/space/list discovery failed.
func printConfigNoPick(pick pickResult) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "── Token ──────────────────────────────────────")
	fmt.Fprintf(os.Stderr, "%s\n", pick.Token)
	fmt.Fprintln(os.Stderr, "───────────────────────────────────────────────")
	fmt.Fprint(os.Stderr, `
Add to your vine configuration:

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

// promptSelect prompts the user to pick an item by number (1-indexed).
// Returns the 0-indexed selection.
func promptSelect(label string, max int) int {
	for {
		fmt.Fprintf(os.Stderr, "%s [1-%d]: ", label, max)
		input, _ := stdin.ReadString('\n')
		input = strings.TrimSpace(input)
		idx, err := strconv.Atoi(input)
		if err == nil && idx >= 1 && idx <= max {
			return idx - 1
		}
		fmt.Fprintf(os.Stderr, "Invalid choice. Enter a number between 1 and %d.\n", max)
	}
}

// promptString prompts the user for a freeform string.
func promptString(label string) string {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	input, _ := stdin.ReadString('\n')
	return strings.TrimSpace(input)
}

// tokenType guesses the auth mode from the token prefix.
func tokenType(token string) string {
	if strings.HasPrefix(token, "pk_") {
		return "personal (pk_ prefix)"
	}
	return "oauth (bearer token)"
}
