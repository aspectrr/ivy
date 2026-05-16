# Ivy — Managed Data Engineering Agents

🌿 Agents that tend to your data pipelines so you don't have to.

## Components

| Binary | Name     | Role                                                                                                                   |
| ------ | -------- | ---------------------------------------------------------------------------------------------------------------------- |
| `vine` | The vine | Main daemon. Manages agent sessions, Docker sandboxes, and orchestrates the agent runtime.                             |
| `leaf` | The leaf | Lightweight daemon. Runs on log parser hosts, executes whitelisted read-only commands, syncs configs back to the vine. |

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

## Architecture

See [PRD.md](PRD.md) for the full product spec and [TASKS.md](TASKS.md) for the task breakdown.

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
         └────────────┬────────────────────┘
                      │ gRPC (leaf initiates)
          ┌───────────┴───────────┐
     ┌────┴─────┐           ┌────┴─────┐
     │   leaf   │           │   leaf   │
     │ (parser  │           │ (parser  │
     │  host 1) │           │  host 2) │
     └──────────┘           └──────────┘
```
