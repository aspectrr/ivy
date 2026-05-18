# Ivy — Managed Data Engineering Agents

🌿 Agents that tend to your data pipelines so you don't have to.

## Components

| Binary | Name     | Role                                                                                                                   |
| ------ | -------- | ---------------------------------------------------------------------------------------------------------------------- |
| `ivy`  | The CLI  | Laptop CLI for setting up integrations. Install via `go install`.                                                      |
| `vine` | The vine | Main daemon. Manages agent sessions, Docker sandboxes, and orchestrates the agent runtime.                             |
| `leaf` | The leaf | Lightweight daemon. Runs on log parser hosts, executes whitelisted read-only commands, syncs configs back to the vine. |

## Architecture

```
         ┌─────────────────────────────────┐
         │            vine                  │
         │  ┌──────────┐ ┌──────────────┐  │
         │  │ Sessions │ │ Orchestrator │  │
         │  └──────────┘ └──────────────┘  │
         │  ┌──────────┐ ┌──────────────┐  │
         │  │ Sandbox  │ │  ClickUp     │  │
         │  │ Manager  │ │  connector   │  │
         │  └──────────┘ └──────────────┘  │
         └────────────────┬────────────────┘
                      │ gRPC (leaf initiates)
          ┌───────────┴───────────┐
     ┌────┴─────┐           ┌────┴─────┐
     │   leaf   │           │   leaf   │
     │ (parser  │           │ (parser  │
     │  host 1) │           │  host 2) │
     └──────────┘           └──────────┘
```

## Build

```bash
make build          # Build vine + leaf + ivy CLI
make build-vine     # Build the vine only
make build-leaf     # Build the leaf only
make build-ivy      # Build the ivy CLI only
make test           # Run tests
make lint           # Run linter
make proto-gen      # Generate protobuf code
make migrate-up     # Run database migrations
make migrate-down   # Rollback database migrations
make docker-build   # Build Docker images
```

## Quick Start

### 1. Prerequisites

- Go 1.22+
- Docker (for agent sandboxes and pipeline testing)
- PostgreSQL 17 with pgvector extension (or use the dev docker-compose)
- A ClickUp account (for task integration)

### 2. Database

Start a dev PostgreSQL instance:

```bash
docker compose -f deploy/docker/docker-compose.dev.yml up -d
make migrate-up
```

### 3. Configure vine

Copy and edit the config:

```bash
cp configs/vine.yaml configs/vine.local.yaml
```

Set your LLM credentials via environment variables:

```bash
export IVY_LLM_API_KEY=your-api-key
export IVY_LLM_ENDPOINT=https://openrouter.ai/api/v1  # or any OpenAI-compatible endpoint
export IVY_LLM_MODEL=mistralai/mistral-medium-3-5
```

### 4. Connect ClickUp

Install the ivy CLI and authenticate with ClickUp:

```bash
go install github.com/aspectrr/ivy/cmd/ivy@latest
ivy auth clickup
```

This opens your browser for OAuth — authorize the workspace(s) you want the agent to access. No extra ClickUp seat needed. Any workspace member can do this (admin is not required, but someone with broad access is recommended).

The CLI prints an access token and instructions for adding it to your vine config.

To validate an existing token:

```bash
IVY_CLICKUP_API_TOKEN=your-token ivy auth clickup --validate
```

For personal tokens (single user, less ideal for enterprise):

```bash
ivy auth clickup --personal
```

### 5. Get the Team ID

1. Open ClickUp in your browser
2. Navigate to any view in your workspace
3. The URL looks like: `https://app.clickup.com/90141261182/v/lis/...`
4. The number after `/app/` is your **Team ID** (e.g., `90141261182`)

Alternatively, find it in **Settings → Team → Team ID**.

### 6. Configure vine

Set the following environment variables:

```bash
# Required — from ivy auth clickup
export IVY_CLICKUP_API_TOKEN=your-token
export IVY_CLICKUP_TEAM_ID=90141261182

# Required for @mention detection — the agent's ClickUp username
export IVY_CLICKUP_AGENT_USERNAME=ivy-agent
```

Or set them in your config file (`configs/vine.yaml`):

```yaml
connectors:
  clickup:
    enabled: true
    auth_mode: oauth          # "oauth" or "personal" — auto-detected from token prefix
    api_token: ""             # Set via IVY_CLICKUP_API_TOKEN env var
    team_id: ""               # Set via IVY_CLICKUP_TEAM_ID env var
    agent_username: ""        # e.g. "ivy-agent" — for @mention detection
    assignee: ""              # e.g. "12345" — the agent's user ID for assignment detection
    poll_interval: "30s"
    # Optional: restrict to specific list or space
    list_id: ""
    space_id: ""
    tag: ""                   # Only process tasks with this tag
```

### 7. Build & Run

```bash
make build
./bin/vine -config configs/vine.local.yaml
```

---

## ClickUp Integration

### How It Works

Ivy connects to ClickUp so people can assign tasks to the agent or `@mention` it in comments. The agent picks up the task with full context (description, comments, attachments) and starts working.

Vine polls ClickUp every 30 seconds (configurable) and reacts to three types of events:

| Trigger                             | What Happens                                      | Example                                                                                   |
| ----------------------------------- | ------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| **Task assigned** to the agent user | Creates a new session with task context           | Someone drags a task and assigns it to "ivy-agent"                                        |
| **@mention** in a comment           | Creates or resumes a session with comment context | Someone comments `@ivy-agent the Logstash pipeline for host-X is failing, can you check?` |
| **Task updated**                    | Resumes an existing session if one exists         | Someone changes the task status or description                                            |

The agent receives the full task context: name, description, status, URL, and all comments. It can then use its tools (sandbox, parser host commands, pipeline testing) to investigate and resolve the issue, posting comments back to the ClickUp task with its findings.

### Usage from ClickUp

People interact with the agent in two ways:

1. **Assign a task**: Create or assign an existing task to the agent user. The agent picks it up and starts working.

2. **@mention in a comment**: Comment `@ivy-agent` on any task (the agent user must have access to that space). The agent will see the full task context plus the mention comment and start a session.

---

## Architecture

See [PRD.md](PRD.md) for the full product spec and [TASKS.md](TASKS.md) for the task breakdown.
