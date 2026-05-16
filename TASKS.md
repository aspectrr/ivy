# Ivy — Task Breakdown & Tracker

> **Status Key:** `[ ]` Pending | `[~]` In Progress | `[x]` Completed
>
> **Naming:** `vine` = main daemon (the vine). `leaf` = daemon on log parser hosts (the leaves).

---

## Phase 1: Foundation — ✅ COMPLETE

### 1.1 — Project Scaffolding
- **Status:** `[x]`
- **Depends On:** None
- **Blocks:** 1.2, 1.3

**Completed:**
- Go module initialized (`github.com/aspectrr/ivy`)
- Directory structure created with `cmd/vine`, `cmd/leaf`, `internal/vine/*`, `internal/leaf/*`, `internal/ivyv1`
- Makefile with targets: `build`, `build-vine`, `build-leaf`, `test`, `lint`, `proto-gen`, `migrate-up`, `migrate-down`, `docker-build`, `clean`
- `.gitignore`, `.golangci.yml`, `.goreleaser.yml`
- `buf.yaml` + `buf.gen.yaml` for protobuf generation
- Both `cmd/vine/main.go` and `cmd/leaf/main.go` entrypoints with config loading, structured logging, signal handling
- GoReleaser config for cross-platform builds (linux/darwin, amd64/arm64) with Homebrew tap support
- `make build` compiles both binaries, 21 tests passing across 6 packages

---

### 1.2 — Database Schema & Migrations
- **Status:** `[x]`
- **Depends On:** 1.1
- **Blocks:** 2.1, 5.2, 5.3

**Completed:**
- PostgreSQL + pgvector migration (`migrations/001_init_schema.sql`) with 5 tables:
  - `sessions` — session truth (id, source, source_id, status, agent_config, sandbox_id, metadata, timestamps)
  - `events` — append-only event log (id, session_id, seq, type, data JSONB, created_at) with UNIQUE(session_id, seq)
  - `skills` — compounding skills (id, name, description, content, embedding vector(1536), source_session_id, built_in, timestamps)
  - `knowledge_entries` — indexed session history (id, session_id, content, embedding vector(1536), metadata, created_at)
  - `skill_usage` — skill usage tracking (id, skill_id, session_id, was_helpful, created_at)
- HNSW vector indexes on `skills.embedding` and `knowledge_entries.embedding`
- GIN index on `events.data` for JSONB queries
- `updated_at` trigger on `sessions` and `skills`
- pgvector extension auto-created in migration
- `goose` for migration management
- Go connection pool manager (`internal/vine/database/pool.go` using `pgxpool`)
- Dev docker-compose (`deploy/docker/docker-compose.dev.yml`) with `pgvector/pgvector:pg17`
- `make migrate-up` / `make migrate-down` verified working

---

### 1.3 — gRPC Protobuf Definitions
- **Status:** `[x]`
- **Depends On:** 1.1
- **Blocks:** 4.1

**Completed:**
- `proto/leaf.proto` with `LeafService`:
  - `Connect(stream LeafMessage) returns (stream VineCommand)` — bidirectional streaming
  - `ExecuteCommand(ExecuteCommandRequest) returns (ExecuteCommandResponse)` — unary fallback
  - `SyncDirectory(SyncDirectoryRequest) returns (SyncDirectoryResponse)` — directory sync
- Leaf is the **client**, vine is the **server** — no inbound ports needed on parser hosts
- Messages: `LeafMessage` (heartbeat, command output, directory chunk, registration), `VineCommand` (execute command, sync directory)
- `CommandType` enum: GREP, AWK, FIND, CAT, READ_FILE, TAIL, SYSTEMCTL_STATUS, JOURNALCTL
- `buf generate` produces Go code into `internal/ivyv1/` (package `ivyv1`)
- `Registration` message for leaf to identify itself on connect (host_id, hostname, allowed_directories)
- Tests for all proto message types in `internal/ivyv1/proto_test.go`

---

## Phase 2: Core Vine Daemon

### 2.1 — Session & Event Store
- **Status:** `[ ]`
- **Depends On:** 1.2
- **Blocks:** 2.3, 2.4, 5.1

**Description:**
Implement the session management and append-only event store. This is the "session truth" and "event history" pillars from the PRD.

**Tasks:**
- [ ] Create `internal/vine/session/store.go` — session CRUD:
  - `Create(source, sourceID, agentConfig) (*Session, error)`
  - `Get(id) (*Session, error)`
  - `GetBySource(source, sourceID) (*Session, error)` — for looking up ClickUp task → session
  - `UpdateStatus(id, status) error`
  - `SetSandboxID(id, sandboxID) error`
  - `ListByStatus(status, limit, offset) ([]Session, error)`
  - `UpdateMetadata(id, metadata) error`
- [ ] Create `internal/vine/eventstore/store.go` — append-only event log:
  - `Append(sessionID, eventType, data) (*Event, error)` — auto-increments seq
  - `GetEvents(sessionID, afterSeq, limit) ([]Event, error)` — pagination
  - `GetLatest(sessionID) (*Event, error)`
  - `GetEventsByType(sessionID, eventType, limit) ([]Event, error)`
  - `StreamEvents(sessionID, afterSeq) (<-chan Event, error)` — for real-time streaming to connectors
- [ ] Define Go types for `Session`, `Event`, `EventType` constants
- [ ] Define JSONB payload types for each event type:
  - `UserMessagePayload{Content, Attachments}`
  - `AgentMessagePayload{Content, Model, TokensUsed}`
  - `ToolCallPayload{ToolName, Args, CallID}`
  - `ToolResultPayload{CallID, Output, IsError}`
  - `InterruptPayload{Reason, RequiresAction}`
  - `StatusTransitionPayload{From, To}`
  - `ErrorPayload{Message, StackTrace, Recoverable}`
- [ ] Unit tests with test database (use `pgxpool` + the dev postgres container)
- [ ] Ensure all operations are within transactions where appropriate

**Acceptance Criteria:**
- All CRUD operations work and are tested
- Event sequence numbers are monotonic per session
- Append-only: no update or delete methods on events
- Pagination works correctly for large event histories

---

### 2.2 — Agent Runtime Orchestration
- **Status:** `[ ]`
- **Depends On:** 2.1
- **Blocks:** 2.3, 2.4

**Description:**
Implement the orchestration layer that manages the agent lifecycle: provision, run, interrupt, resume, retry. This talks to an OpenAI-compatible API (configured in `configs/vine.yaml`).

**Tasks:**
- [ ] Create `internal/vine/orchestrator/orchestrator.go` with interface:
  - `StartRun(sessionID) error` — create sandbox, load agent config, start agent loop
  - `Interrupt(sessionID) error` — graceful interrupt (agent gets to save state)
  - `Resume(sessionID) error` — resume after human confirmation
  - `Retry(sessionID) error` — retry from last checkpoint
  - `Suspend(sessionID) error` — suspend (keep sandbox alive, pause agent loop)
  - `Terminate(sessionID) error` — kill sandbox, mark session completed/failed
- [ ] Implement the **agent loop**:
  ```
  loop:
    1. Build context: system prompt + skills + history + session events
    2. Call LLM (OpenAI-compatible API)
    3. If tool_call → execute tool → append tool_result event → goto 1
    4. If agent_message → append event → stream to connector (ClickUp)
    5. If requires_action → suspend run, notify connector
    6. If done → append status_transition → break
  ```
- [ ] Create `internal/vine/orchestrator/llm_client.go`:
  - OpenAI-compatible API client
  - Support for streaming responses
  - Tool/function calling support
  - Configurable model, temperature, max tokens from session agent config
- [ ] Create `internal/vine/orchestrator/context_builder.go`:
  - Builds the message array for the LLM from session events
  - Injects system prompt with available tools
  - Injects relevant skills (based on task description)
  - Manages context window limits (summarize older events if needed)
- [ ] Wire into `cmd/vine/main.go` (config loading already done in `internal/vine/config/config.go`)
- [ ] Add graceful shutdown handling (persist state, terminate sandboxes)
- [ ] Unit tests for agent loop with mocked LLM client

**Acceptance Criteria:**
- Agent loop can complete a full cycle: start → tool call → tool result → final message
- Interrupt/resume work correctly
- Context builder respects token limits
- Config loads from YAML with env var interpolation

---

### 2.3 — Docker Sandbox Manager (Vine)
- **Status:** `[ ]`
- **Depends On:** 2.2
- **Blocks:** 2.4, 3.1

**Description:**
Manage the lifecycle of Docker containers that serve as agent workspaces. Each running session gets a dedicated container. Lives in `internal/vine/vine/`.

**Tasks:**
- [ ] Create `internal/vine/vine/manager.go` with interface:
  - `Create(sessionID, image, config) (Sandbox, error)` — spin up container
  - `Get(sessionID) (Sandbox, error)`
  - `List() ([]Sandbox, error)`
  - `Destroy(sessionID) error`
  - `CleanupIdle(timeout) error` — garbage collect idle sandboxes
- [ ] Create `internal/vine/vine/sandbox.go` — Sandbox type:
  - `ID` — Docker container ID
  - `SessionID` — associated session
  - `ContainerIP` — internal Docker network IP
  - `CreatedAt`, `LastUsedAt`
  - `Exec(cmd, args) (stdout, stderr, exitCode, error)` — execute inside container
  - `WriteFile(path, content) error`
  - `ReadFile(path) (string, error)`
- [ ] Implement container creation with:
  - No network access (`--network=none` or internal-only Docker network)
  - Resource limits (CPU, memory — configurable)
  - Volume mounts for workspace persistence if needed
  - Labels for tracking (`ivy-session-id`, `ivy-type`)
  - Health checks
- [ ] Create Docker network for pipeline sandboxes (Kafka, Logstash, ES need to talk)
- [ ] Implement `Exec` using Docker SDK (`containerExecCreate` + `containerExecAttach`)
- [ ] Implement file operations using Docker cp API or exec with tar
- [ ] Background goroutine for idle sandbox cleanup
- [ ] Build `deploy/docker/agent-sandbox.Dockerfile` — the agent workspace image:
  - Base: debian/ubuntu slim
  - Pre-installed: python3, common data engineering tools
  - Workspace directory: `/workspace`
- [ ] Integration tests (requires Docker)

**Acceptance Criteria:**
- Can create, exec into, and destroy containers
- Containers have no outbound network access
- Multiple sandboxes can run concurrently
- Idle cleanup works correctly
- Resource limits are enforced

---

### 2.4 — Tool Execution Framework
- **Status:** `[ ]`
- **Depends On:** 2.2, 2.3
- **Blocks:** 3.2, 3.3, 4.2

**Description:**
Build the tool registry and dispatch system that the agent uses. This is what translates LLM tool calls into actual operations.

**Tasks:**
- [ ] Create `internal/vine/tools/registry.go`:
  - `Register(name, tool)` — register a tool
  - `Get(name) (Tool, error)` — look up a tool
  - `List() []ToolDef` — return tool definitions for LLM function calling schema
  - `Execute(name, args) (result, error)` — dispatch a tool call
- [ ] Define `Tool` interface:
  ```go
  type Tool interface {
    Definition() ToolDef       // JSON schema for LLM
    Execute(args json.RawMessage, ctx ToolContext) (json.RawMessage, error)
  }
  type ToolDef struct {
    Name        string
    Description string
    Parameters  json.RawMessage  // JSON Schema
  }
  type ToolContext struct {
    SessionID   string
    Sandbox     *vine.Sandbox    // nil if no sandbox
    EventStore  *eventstore.Store
  }
  ```
- [ ] Implement workspace sandbox tools:
  - `sandbox_read_file` — read file from agent sandbox
  - `sandbox_write_file` — write file to agent sandbox
  - `sandbox_bash` — execute bash in agent sandbox
  - `sandbox_create_pipeline` — spin up pipeline sandbox (Kafka + Logstash + ES)
- [ ] Implement search tools:
  - `search_history` — search past session events (text + vector)
  - `search_skills` — search skills by name/description/embedding
- [ ] Implement execute tool (meta-tool):
  - `execute_tool` — lets agent dispatch sub-tools, this is the main dispatch entry
- [ ] Implement log parser host tools:
  - `parser_grep` — run grep on parser host (via gRPC to leaf)
  - `parser_awk` — run awk on parser host
  - `parser_find` — find on parser host
  - `parser_cat` — cat on parser host
  - `parser_read_file` — read specific file on parser host
  - `parser_tail` — tail on parser host
  - `parser_systemctl_status` — systemctl status on parser host
  - `parser_journalctl` — journalctl on parser host
  - These all route through the gRPC connection to the appropriate leaf
- [ ] Wire tools into the orchestrator's agent loop
- [ ] Unit tests for each tool with mocked dependencies

**Acceptance Criteria:**
- Tool registry can register and dispatch tools
- Tool definitions are valid JSON Schema for LLM function calling
- All workspace tools work against a real Docker sandbox
- Parser host tools route through gRPC client

---

## Phase 3: Pipeline Sandbox

### 3.1 — Pipeline Sandbox Infrastructure
- **Status:** `[ ]`
- **Depends On:** 2.3
- **Blocks:** 3.2, 3.3

**Description:**
Build the sandboxed Kafka → Logstash → Elasticsearch pipeline that agents use to test their work end-to-end.

**Tasks:**
- [ ] Create `deploy/docker/pipeline-sandbox-compose.yml`:
  - Kafka container (single broker, KRaft mode — no ZooKeeper needed)
  - Elasticsearch container (single node, with security disabled for sandbox)
  - Logstash container (configurable pipelines)
  - All on a shared Docker internal network
- [ ] Create `internal/vine/vine/pipeline.go`:
  - `CreatePipelineSandbox(sessionID, parserConfig) (PipelineSandbox, error)`
    - Spins up Kafka + Logstash + ES containers on a dedicated Docker network
    - Injects the parser's Logstash config (rewritten with Docker hostnames)
    - Waits for all services to be healthy
  - `DestroyPipelineSandbox(sessionID) error`
  - `SendData(sandboxID, topic, data) error` — produce to Kafka
  - `QueryES(sandboxID, index, query) (results, error)` — query Elasticsearch
- [ ] Implement config rewriting (`internal/vine/vine/config_rewrite.go`):
  - Parse Logstash config files
  - Replace Kafka bootstrap servers in `input {}` blocks with `kafka:9093`
  - Replace ES hosts in `output {}` blocks with `elasticsearch:9200`
  - Use regex or simple parser for the substitutions
  - Preserve everything else (filters, grok patterns, etc.)
- [ ] Create Docker image build targets in Makefile
- [ ] Integration tests: spin up pipeline, send data, verify ES output

**Acceptance Criteria:**
- Pipeline sandbox spins up a working Kafka → Logstash → ES pipeline
- Config rewriting correctly replaces hostnames
- Data flows through: produce to Kafka → processed by Logstash → indexed in ES
- Sandbox cleanup removes all containers and networks

---

### 3.2 — Pipeline Tools
- **Status:** `[ ]`
- **Depends On:** 2.4, 3.1
- **Blocks:** None

**Description:**
Expose pipeline sandbox operations as tools the agent can call.

**Tasks:**
- [ ] `pipeline_send_data` tool — agent sends test data through Kafka:
  - Args: `session_id`, `topic`, `data` (raw string or JSON), `format` (plain, json, syslog)
  - Produces to the sandbox Kafka broker
  - Waits briefly for Logstash to process
  - Returns confirmation
- [ ] `pipeline_query_es` tool — agent queries the sandbox ES:
  - Args: `session_id`, `index` (optional), `query` (ES query DSL or simple text search)
  - Returns matching documents
- [ ] `pipeline_get_logstash_status` tool — check if Logstash is processing:
  - Returns Logstash logs, pipeline status, any errors
- [ ] `pipeline_update_config` tool — agent updates the Logstash config:
  - Args: `session_id`, `config_content`
  - Rewrites config with Docker hostnames
  - Restarts Logstash container
  - Returns status
- [ ] Wire these into the tool registry

**Acceptance Criteria:**
- Agent can send data and query results end-to-end
- Config updates trigger Logstash restart
- Error states are surfaced to the agent clearly

---

### 3.3 — Sandbox Data Flow Integration
- **Status:** `[ ]`
- **Depends On:** 2.4, 3.1, 5.1
- **Blocks:** None

**Description:**
The end-to-end flow where the agent asks the user for data, receives it via the ClickUp task, and uses it to test in the pipeline sandbox.

**Tasks:**
- [ ] Implement attachment/file handling in the ClickUp connector:
  - When a user uploads a file to the ClickUp task, download and store it
  - Make it available in the agent sandbox workspace
  - Post a `user_message` event with the file reference
- [ ] Implement the agent's "ask user" flow:
  - Agent sends a message via the tool (or natural message) asking for data
  - This creates an `interrupt` event with `requires_action`
  - Connector posts the question to ClickUp
  - User responds (with text or file attachment)
  - Connector picks up the response, creates `user_message` event
  - Orchestrator resumes the run
- [ ] Agent sandbox can read the uploaded data files
- [ ] Integration test: full flow from agent request → user upload → sandbox processing

**Acceptance Criteria:**
- Agent can request data from the user
- User can upload files via ClickUp
- Files appear in the agent sandbox
- Run resumes automatically after user provides data

---

## Phase 4: Leaf Daemon

### 4.1 — Leaf Daemon Core
- **Status:** `[ ]`
- **Depends On:** 1.3
- **Blocks:** 4.2, 4.3

**Description:**
Build the lightweight leaf daemon that runs on each log parser host. It connects to vine via gRPC (bidirectional streaming), executes whitelisted read-only commands, and syncs directory contents.

**Tasks:**
- [ ] Flesh out `cmd/leaf/main.go`:
  - Connect to vine gRPC endpoint with mTLS
  - Establish bidirectional stream
  - Handle commands from stream
  - Heartbeat / keepalive
- [ ] Create `internal/leaf/commands/executor.go`:
  - `Execute(commandType, args) (CommandResult, error)`
  - Strict command whitelist — reject anything not in the allowed list
  - No shell expansion, no pipes, no subshells — each command is direct exec
  - Working directory restricted to allowed directories
  - Timeout enforcement
  - Output size limits (prevent dumping huge files)
- [ ] Implement each whitelisted command:
  - `grep` — only read-only flags (`-r`, `-i`, `-n`, `-c`, `-v`, `-l`)
  - `awk` — basic awk programs (validate no system() or pipe)
  - `find` — search within allowed directories only
  - `cat` — only files within allowed directories
  - `read_file` — direct file read with path validation
  - `tail` — with `-n`, `-f` (timeout-bound)
  - `systemctl status` — only the status subcommand, only for logstash services
  - `journalctl` — read-only flags only (`-u`, `-n`, `--since`, `--until`, `--no-pager`)
- [ ] Create `internal/leaf/sync/sync.go`:
  - Stream directory contents to vine on request
  - Tar + stream files from allowed directories
  - Checksum verification
  - Incremental sync (only changed files)
- [ ] Path validation function — resolve symlinks, ensure within allowed directories
  - Reject `..`, symlinks outside allowed dirs, `/proc`, `/sys`, etc.
- [ ] Logging — all commands executed are logged with timestamp, command, args, result summary
- [ ] Systemd unit file for leaf

**Acceptance Criteria:**
- Leaf connects to vine via gRPC with mTLS
- All whitelisted commands execute correctly
- Any command outside the whitelist is rejected
- Path traversal attempts are blocked
- Directory sync works for allowed directories
- Runs as its own user with minimal permissions

---

### 4.2 — Parser Host Tools (Vine Side)
- **Status:** `[ ]`
- **Depends On:** 2.4, 4.1
- **Blocks:** None

**Description:**
The vine side of the parser host tools. Routes tool calls from the agent through gRPC to the appropriate leaf.

**Tasks:**
- [ ] Create `internal/vine/tools/parser_client.go`:
  - Maintains connections to registered leaves
  - Routes commands to the right leaf based on session → parser mapping
  - Handles connection drops and reconnection
- [ ] Create leaf registry:
  - Track which leaves are connected (via gRPC stream metadata / Registration message)
  - Session can be associated with a specific parser host
  - Fallback / error if the leaf is disconnected
- [ ] Wire parser tools into tool registry (from 2.4)

**Acceptance Criteria:**
- Agent tool calls route to the correct leaf
- Disconnected leaves surface clear errors
- Multiple leaves can be connected simultaneously

---

### 4.3 — Ansible Deployment Playbook & SECURITY.md
- **Status:** `[ ]`
- **Depends On:** 4.1
- **Blocks:** None

**Description:**
Create auditable, human-readable deployment automation and a thorough security analysis document.

**Tasks:**
- [ ] Create `deploy/ansible/inventory.example.yml`
- [ ] Create `deploy/ansible/deploy-leaf.yml` playbook:
  - Create `ivy-leaf` system user (no login shell, no home directory write access)
  - Create necessary directories with restrictive permissions
  - Install `leaf` binary
  - Generate or deploy mTLS certificates
  - Install config file (`/etc/ivy-leaf/config.yaml`)
  - Install systemd unit
  - Set up filesystem ACLs (read-only access to allowed directories)
  - Optionally: deploy AppArmor/SELinux profile
  - Start and enable service
  - Verify connectivity to vine
- [ ] Create `deploy/ansible/deploy-vine.yml` playbook:
  - Install Docker if not present
  - Create `ivy` system user
  - Install `vine` binary
  - Install config (`/etc/ivy/config.yaml`)
  - Generate or deploy mTLS certificates (including CA for leaves)
  - Install systemd unit
  - Run database migrations
  - Pull/build sandbox Docker images
  - Start and enable service
- [ ] Create AppArmor profile (`deploy/apparmor/ivy-leaf`):
  - Allow reading only from allowed directories
  - Allow executing only whitelisted commands
  - Deny network access except to vine
  - Deny write access except to own log/config directory
- [ ] Create `SECURITY.md` with sections:
  - **Threat Model** — what we're protecting against
  - **Leaf Daemon Security** — why the permissions are minimal, what's whitelisted, audit trail
  - **Network Security** — mTLS, no inbound ports on parser hosts, Docker network isolation
  - **Agent Sandbox Security** — no network, resource limits, ephemeral by default
  - **Credential Boundaries** — where secrets live, what has access to what
  - **Audit & Logging** — what's logged, retention, how to review
  - **Deployment Security** — IaC benefits, human auditable playbooks, mTLS certificate rotation
  - **Known Limitations** — what the MVP does NOT protect against
- [ ] Add `deploy/ansible/vars/main.yml` for configurable parameters

**Acceptance Criteria:**
- Ansible playbooks are idempotent and can be run repeatedly
- SECURITY.md covers all major security considerations
- Leaf runs as its own user with minimal permissions
- All security decisions are documented and justified

---

## Phase 5: Connectors & Knowledge

### 5.1 — ClickUp Connector
- **Status:** `[ ]`
- **Depends On:** 2.1, 2.2
- **Blocks:** 3.3

**Description:**
Build the ClickUp integration that maps ClickUp tasks to Ivy sessions. This is the primary user interface for MVP.

**Tasks:**
- [ ] Create `internal/vine/connector/clickup/connector.go`:
  - Webhook handler for ClickUp task events
  - Task created → create new session
  - Task comment (by assignee) → append user_message event, resume run if suspended
  - Task status change → map to session status changes
- [ ] Create `internal/vine/connector/clickup/client.go`:
  - ClickUp API v2 client
  - Post comment to task (agent messages)
  - Update task status
  - Download attachments
  - Get task details (name, description, assignee, custom fields)
- [ ] Implement webhook server:
  - Verify ClickUp webhook signature
  - Parse webhook payload
  - Route to appropriate handler
  - Handle retries / idempotency (dedup by webhook event ID)
- [ ] Implement session ↔ task mapping:
  - Task ID stored in `sessions.source_id`
  - Task description becomes initial context for agent
  - Task assignee is the "user" for the session
  - Task comments become user messages
  - Agent responses posted as comments
  - File attachments downloaded and placed in sandbox
- [ ] Create webhook endpoint in vine HTTP server (alongside gRPC)
- [ ] Handle the "agent asks user" flow:
  - Agent message with `requires_action` → post as ClickUp comment
  - User replies → picked up via webhook → resume session

**Acceptance Criteria:**
- New ClickUp task triggers a new Ivy session
- Comments on the task are forwarded to the agent
- Agent responses appear as task comments
- File attachments are downloaded and available in the sandbox
- Webhook signature verification works
- Idempotent webhook handling

---

### 5.2 — Skill System
- **Status:** `[ ]`
- **Depends On:** 1.2, 2.1
- **Blocks:** None

**Description:**
Build the compounding skill system. Agents create skills after completing sessions, and search for relevant skills when starting new sessions.

**Tasks:**
- [ ] Create `internal/vine/skills/store.go`:
  - `Create(name, description, content, sourceSessionID) (*Skill, error)`
  - `Get(id) (*Skill, error)`
  - `GetByName(name) (*Skill, error)`
  - `Search(query, limit) ([]Skill, error)` — vector similarity search
  - `Update(id, content) error`
  - `Delete(id) error`
  - `RecordUsage(skillID, sessionID) error`
  - `MarkHelpful(usageID, helpful) error`
- [ ] Implement embedding generation:
  - Use the same OpenAI-compatible endpoint to generate embeddings
  - Embed skill name + description for search
  - Store in pgvector `embedding` column
  - Use cosine similarity for search
- [ ] Create built-in skills in `skills/` directory:
  - `kafka-skills/SKILL.md` — Kafka debugging patterns
  - `elasticsearch-skills/SKILL.md` — ES query patterns, mapping debugging
  - `logstash-skills/SKILL.md` — Logstash config patterns, grok debugging
  - `sysadmin-skills/SKILL.md` — Common system debugging patterns
  - `create-skill/SKILL.md` — Instructions for the agent on how to create new skills
- [ ] Implement skill loading at startup:
  - Load built-in skills from `skills/` directory
  - Seed into database if not already present
  - Generate embeddings for any new skills
- [ ] Implement "create skill" tool for the agent:
  - `skill_create` — agent creates a new skill at end of session
  - Prompt the agent to: reflect on what it did, what worked, what didn't, summarize
  - Auto-generate name and description
  - Agent writes the skill content
- [ ] Implement skill search tool:
  - `skill_search` — agent searches for relevant skills
  - Takes a query string
  - Returns top-k similar skills with content
- [ ] Wire into orchestrator:
  - At session start, automatically search for relevant skills based on task description
  - Inject relevant skills into the agent's context/system prompt
  - At session end, nudge agent to create a skill (add to system prompt)

**Acceptance Criteria:**
- Built-in skills load at startup
- Vector search returns relevant skills
- Agent can create new skills via tool
- Skills are injected into agent context at session start
- Skill usage tracking works

---

### 5.3 — History Search
- **Status:** `[ ]`
- **Depends On:** 1.2, 2.1
- **Blocks:** None

**Description:**
Implement searchable session history using both semantic (vector) and structured search. Agents are nudged to search history when facing unfamiliar situations.

**Tasks:**
- [ ] Create `internal/vine/history/store.go`:
  - `IndexSession(sessionID) error` — generate embeddings for key session events and store
  - `Search(query, limit) ([]HistoryEntry, error)` — vector similarity search
  - `SearchByFilter(filters, limit, offset) ([]Session, error)` — structured search (date, source, status, tools used)
- [ ] Implement session indexing:
  - At session completion, extract key events (user messages, agent messages with tool calls, final result)
  - Generate embeddings for summary/key moments
  - Store in `knowledge_entries` table
  - Alternative: index the full session transcript in chunks with embeddings
- [ ] Create `search_history` tool:
  - Args: `query` (text), `limit` (optional, default 5)
  - Returns: matching past sessions with summaries and key takeaways
  - Agent is nudged in system prompt to search when uncertain
- [ ] Create session summarizer:
  - At session end, use LLM to generate a summary of what happened
  - Store summary in session metadata
  - Use summary for history indexing (lighter than indexing all events)
- [ ] Add structured search filters:
  - By date range
  - By source (clickup task ID)
  - By tools used
  - By outcome (success/failure)

**Acceptance Criteria:**
- Completed sessions are indexed automatically
- Vector search returns relevant past sessions
- Structured search works with filters
- Agent tool is available and functional
- Search results include enough context for the agent to learn from

---

## Phase 6: Integration & Polish

### 6.1 — End-to-End Integration
- **Status:** `[ ]`
- **Depends On:** All previous phases
- **Blocks:** 6.2

**Description:**
Wire everything together and test the complete flow end-to-end.

**Tasks:**
- [ ] Full integration test:
  1. ClickUp task created → session created
  2. Agent starts → searches skills + history
  3. Agent creates sandbox → reads parser config from leaf
  4. Agent creates pipeline sandbox → tests config
  5. Agent sends data through pipeline → verifies ES output
  6. Agent posts results to ClickUp
  7. Session completes → skill created → history indexed
- [ ] Error handling and recovery:
  - LLM API failures → retry with backoff
  - Docker failures → clean up, report to agent
  - gRPC disconnections → reconnect, notify agent
  - Database failures → graceful degradation
- [ ] Monitoring and observability:
  - Structured logging (slog — already in place)
  - Prometheus metrics (sessions active, tools called, LLM tokens, sandbox count)
  - Health check endpoint
- [ ] Documentation:
  - README with setup instructions
  - Architecture diagram
  - Configuration reference

**Acceptance Criteria:**
- Full flow works from ClickUp task to completed session
- Error recovery works for common failure modes
- Monitoring metrics are exposed
- Documentation is sufficient for a new developer to understand the system

---

### 6.2 — Security Hardening & Production Readiness
- **Status:** `[ ]`
- **Depends On:** 6.1
- **Blocks:** None

**Description:**
Final security review, hardening, and production readiness checklist.

**Tasks:**
- [ ] Security audit of leaf:
  - Verify all commands are truly read-only
  - Test path traversal attacks
  - Verify mTLS certificate validation
  - Test with AppArmor profile enabled
- [ ] Security audit of agent sandboxes:
  - Verify no network access
  - Verify resource limits are enforced
  - Test container escape scenarios (to the extent possible)
- [ ] Credential management:
  - All secrets via environment variables or secret files
  - No secrets in config files, logs, or error messages
  - mTLS certificate rotation procedure documented
- [ ] Backup and recovery:
  - Database backup procedure
  - Session recovery after vine restart
  - Leaf reconnection behavior
- [ ] Performance testing:
  - Concurrent session limits
  - Memory and CPU profiling under load
  - Database query optimization

**Acceptance Criteria:**
- All security measures verified and documented
- No secrets leaked in logs or error messages
- System recovers cleanly from restarts
- Performance is acceptable for expected load

---

## Current State

### What's built
- **`vine`** binary — main daemon entrypoint with config loading, structured logging, signal handling
- **`leaf`** binary — leaf daemon entrypoint with config loading, structured logging, signal handling
- **PostgreSQL schema** — 5 tables (sessions, events, skills, knowledge_entries, skill_usage) with pgvector indexes
- **gRPC protobuf** — `LeafService` with bidirectional streaming, command execution, directory sync
- **GoReleaser** — cross-platform builds for vine + leaf
- **Dev environment** — docker-compose with pgvector/postgres:17
- **21 tests passing** across 6 packages

### Directory structure
```
ivy/
├── cmd/
│   ├── vine/main.go          # Main daemon entrypoint
│   └── leaf/main.go          # Leaf daemon entrypoint
├── internal/
│   ├── vine/
│   │   ├── config/           # Config loading + tests ✅
│   │   ├── database/         # Connection pool + tests ✅
│   │   ├── session/          # Session CRUD (empty)
│   │   ├── eventstore/       # Append-only event log (empty)
│   │   ├── orchestrator/     # Agent runtime loop (empty)
│   │   ├── tools/            # Tool registry & dispatch (empty)
│   │   ├── connector/clickup/# ClickUp integration (empty)
│   │   ├── skills/           # Skill system (empty)
│   │   ├── history/          # Vector/semantic search (empty)
│   │   └── vine/             # Sandbox manager (empty)
│   ├── leaf/
│   │   ├── config/           # Config loading + tests ✅
│   │   ├── commands/         # Whitelisted command executor (empty)
│   │   └── sync/             # Directory sync (empty)
│   └── ivyv1/                # Generated protobuf + tests ✅
├── proto/leaf.proto          # gRPC service definitions
├── migrations/001_init_schema.sql
├── configs/{vine,leaf}.yaml
├── deploy/
│   ├── docker/docker-compose.dev.yml
│   └── ansible/roles/{vine,leaf}/
├── skills/                   # Built-in skill dirs (empty)
├── Makefile, buf.yaml, buf.gen.yaml
├── .goreleaser.yml, .golangci.yml
└── go.mod (github.com/aspectrr/ivy)
```

### Next up
**Phase 2 starts here.** Begin with **2.1 — Session & Event Store** since it has no further blockers (1.1 and 1.2 are complete). This is the foundation that 2.2, 2.3, 2.4, 5.1, 5.2, and 5.3 all depend on.

---

## Dependency Graph

```
Phase 1 (DONE): 1.1 → 1.2, 1.3

Phase 2:
  2.1 (depends: 1.2 ✅) → 2.2 → {2.3, 2.4}
  2.3 → 3.1
  2.4 → {3.2, 3.3, 4.2}

Phase 3:
  3.1 (depends: 2.3) → {3.2, 3.3}
  3.3 (also depends: 5.1)

Phase 4:
  4.1 (depends: 1.3 ✅) → {4.2, 4.3}

Phase 5:
  5.1 (depends: 2.1, 2.2) → 3.3
  5.2 (depends: 1.2 ✅, 2.1)
  5.3 (depends: 1.2 ✅, 2.1)

Phase 6: 6.1 (all above) → 6.2
```

## Estimated Effort

| Phase | Tasks | Est. Time |
|-------|-------|-----------|
| Phase 1: Foundation | 1.1, 1.2, 1.3 | ~~1-2 weeks~~ ✅ Done |
| Phase 2: Core Vine Daemon | 2.1, 2.2, 2.3, 2.4 | 3-4 weeks |
| Phase 3: Pipeline Sandbox | 3.1, 3.2, 3.3 | 2-3 weeks |
| Phase 4: Leaf Daemon | 4.1, 4.2, 4.3 | 2 weeks |
| Phase 5: Connectors & Knowledge | 5.1, 5.2, 5.3 | 2-3 weeks |
| Phase 6: Integration & Polish | 6.1, 6.2 | 1-2 weeks |
| **Total MVP** | | **10-14 weeks remaining** |
