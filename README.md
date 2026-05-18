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

The Ivy OAuth app is pre-configured in the CLI. Each organization just runs:

```bash
go install github.com/aspectrr/ivy/cmd/ivy@latest
ivy auth clickup
```

This opens your browser for OAuth — you authorize your ClickUp workspace (no extra seat needed, any member can do this). Then the CLI walks you through:

1. **Pick team** — auto-selected if you only have one workspace
2. **Pick space** — choose where the agent should respond to `@mentions`
3. **Pick list** — scope to a specific list, or select "all" for the entire space

After selection, the CLI prints a complete config snippet with all IDs filled in — just paste it into your vine config.

Each organization gets their own access token scoped to their workspace. No shared credentials, no inbound access to the vine host needed.

To validate an existing token:

```bash
IVY_CLICKUP_API_TOKEN=your-token ivy auth clickup --validate
```

For personal tokens (single user, less ideal for enterprise):

```bash
ivy auth clickup --personal
```

### 5. Build & Run

```bash
make build
./bin/vine -config configs/vine.local.yaml
```

---

## ClickUp Integration

### How It Works

Ivy connects to ClickUp so people can assign tasks to the agent or `@mention` it in comments. The agent picks up the task with full context (description, comments, attachments) and starts working.

The agent is scoped to a specific space and list (configured during setup). Only @mentions and assignments within that scope trigger the agent — it won't interfere with the rest of your workspace.

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
