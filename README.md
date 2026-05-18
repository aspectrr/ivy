# Ivy — Managed Data Engineering Agents

🌿 Agents that tend to your data pipelines so you don't have to.

## Components

| Binary | Name     | Role                                                                                                                   |
| ------ | -------- | ---------------------------------------------------------------------------------------------------------------------- |
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
make build          # Build vine + leaf
make build-vine     # Build the vine only
make build-leaf     # Build the leaf only
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

### 4. Build & Run

```bash
make build
./bin/vine -config configs/vine.local.yaml
```

---

## ClickUp Integration Setup

Ivy connects to ClickUp so that people can assign tasks to the agent or `@mention` it in comments. The agent picks up the task with full context (description, comments, attachments) and starts working.

### Step 1: Create a Dedicated ClickUp User for the Agent

Create a new ClickUp account for the agent (e.g., `ivy-agent@yourcompany.com`). This gives you:

- A real user that people can **assign tasks to**
- A real user that people can **`@mention`** in comments
- An API token that authenticates as that user

> **Note:** This uses a full ClickUp seat. If you're on a free plan, you can use a secondary email. For teams, consider a shared service account.

### Step 2: Get the API Token

1. Log in to ClickUp as the agent user
2. Go to **Settings → Apps → API Token**
3. Click **Generate** (or copy the existing token — it starts with `pk_`)
4. This is a personal API token that **never expires**

### Step 3: Invite the Agent to Your Workspace

1. Invite the agent user to your ClickUp workspace
2. Give it access to the relevant **Spaces** and **Lists** where pipeline tasks live
3. The agent only sees tasks in spaces it has been added to

### Step 4: Get the Team ID

1. Open ClickUp in your browser
2. Navigate to any view in your workspace
3. The URL looks like: `https://app.clickup.com/90141261182/v/lis/...`
4. The number after `/app/` is your **Team ID** (e.g., `90141261182`)

Alternatively, find it in **Settings → Team → Team ID**.

### Step 5: Get the Agent User ID

You'll need the agent user's ClickUp user ID for the `assignee` filter. You can find it by:

1. Going to the agent user's profile in ClickUp
2. The URL will contain the user ID, or check **Settings → Users**

### Step 6: Configure vine

Set the following environment variables:

```bash
# Required
export IVY_CLICKUP_API_TOKEN=pk_210168233_XXXXXXXXXXXXXXXXXXXXXXXXXX
export IVY_CLICKUP_TEAM_ID=90141261182

# Required for @mention detection — the agent's ClickUp username
export IVY_CLICKUP_AGENT_USERNAME=ivy-agent

# Required for task assignment detection — the agent's ClickUp user ID
# This is optional but recommended; it lets the poller filter by assignee
```

Or set them in your config file (`configs/vine.yaml`):

```yaml
connectors:
  clickup:
    enabled: true
    api_token: "" # Set via IVY_CLICKUP_API_TOKEN env var
    team_id: "" # Set via IVY_CLICKUP_TEAM_ID env var
    agent_username: "" # e.g. "ivy-agent" — for @mention detection
    assignee: "" # e.g. "12345" — the agent's user ID for assignment detection
    poll_interval: "30s"
    # Optional: restrict to specific list or space
    list_id: ""
    space_id: ""
    tag: "" # Only process tasks with this tag
```

### How It Works

Once configured, vine polls ClickUp every 30 seconds (configurable) and reacts to three types of events:

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
