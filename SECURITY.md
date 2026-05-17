# Ivy Security Architecture

## Threat Model

Ivy is designed to protect against these specific threats:

| Threat                           | Mitigation                                            |
| -------------------------------- | ----------------------------------------------------- |
| Agent escapes sandbox            | Docker isolation, `--network=none`, resource limits   |
| Attacker compromises parser host | Leaf runs as dedicated user, read-only commands only  |
| Man-in-the-middle on gRPC        | mTLS between leaf and vine (planned)                  |
| Malicious Logstash config        | Pipeline sandbox is isolated, no access to production |
| Credential leakage               | Secrets via env vars/files, never in logs             |
| LLM prompt injection             | Sandboxed execution, agent cannot affect production   |

### Out of Scope (MVP)

- Protection against compromised vine host (assumes trusted infrastructure)
- Protection against malicious Docker images (uses official images only)
- Protection against kernel-level container escapes
- Multi-tenant isolation (single-organization deployment)

---

## Leaf Daemon Security

The leaf daemon runs on production log parser hosts — the highest-risk surface.

### Principle of Least Privilege

- Runs as `ivy-leaf` system user (no login shell, no home directory)
- Filesystem ACLs grant read-only access to allowed directories only
- AppArmor profile restricts file access and command execution
- No write access to any production files

### Command Whitelist

Only 7 commands are allowed, each with strict flag filtering:

| Command      | Allowed Flags                                  | Blocked                              |
| ------------ | ---------------------------------------------- | ------------------------------------ |
| `grep`       | `-r`, `-i`, `-n`, `-c`, `-v`, `-l`, `-E`, `-F` | `--exec`, any unknown flag           |
| `awk`        | Basic programs only                            | `system()`, pipes, `coproc`          |
| `find`       | `-name`, `-type`, `-maxdepth`, etc.            | `-exec`, `-delete`                   |
| `cat`        | No flags                                       | —                                    |
| `tail`       | `-n`, `-f` (timeout-bound)                     | —                                    |
| `systemctl`  | `status` subcommand only                       | `start`, `stop`, `restart`, `enable` |
| `journalctl` | `-u`, `-n`, `--since`, `--until`, `--no-pager` | `--vacuum-*`, `--unset`              |

### Execution Constraints

- **No shell**: Commands execute directly via `exec.CommandContext`, not through `/bin/sh`
- **No pipes or subshells**: Each command is a direct binary invocation
- **Timeout**: All commands have a configurable timeout (default 30s)
- **Output limit**: 1MB max combined stdout/stderr
- **Working directory**: Must be within allowed directories

### Path Validation

- All file paths validated before command execution
- Symlinks resolved via `filepath.EvalSymlinks`
- Path traversal (`..`) blocked
- `/proc`, `/sys`, `/dev`, `/run` blocked
- Symlinks that escape allowed directories are rejected

### Audit Trail

- Every command execution is logged with timestamp, command, args, and result summary
- Structured JSON logging via `slog`
- Logs written to `/var/log/ivy-leaf/`

---

## Network Security

### gRPC Communication

- **Leaf initiates connection to vine** — no inbound ports needed on parser hosts
- Bidirectional streaming over gRPC
- Leaf reconnects automatically with configurable backoff

### mTLS (Planned)

- Leaf authenticates to vine with client certificate
- Vine authenticates to leaf with server certificate
- CA certificate validates both ends
- Certificate rotation procedure documented in deployment runbook

### Docker Network Isolation

- Agent sandboxes: `--network=none` (no network access at all)
- Pipeline sandboxes: dedicated Docker network per session
  - Redpanda, Logstash, ES on internal network
  - ES exposed on random localhost port for querying
  - No access to production network, other sandboxes, or the internet

---

## Agent Sandbox Security

### Container Isolation

- Each session gets a dedicated Docker container
- Base image: `debian:bookworm-slim` with Python 3
- `--network=none`: no outbound network access
- Resource limits: CPU shares and memory limits enforced
- Ephemeral: containers destroyed when sessions end
- Labels for tracking: `ivy-session-id`, `ivy-type`

### Pipeline Sandbox

- Separate Docker network per pipeline session
- Redpanda, Elasticsearch, Logstash containers
- Config rewriting prevents access to production hosts
- Random port binding prevents port conflicts
- All containers cleaned up on session end

---

## Credential Boundaries

```
┌─────────────────────────────────────────────┐
│                 vine host                    │
│                                             │
│  vine binary ──► PostgreSQL (local)         │
│  vine binary ──► Docker socket              │
│  vine binary ──► LLM API key               │
│  vine binary ──► ClickUp API token          │
│                                             │
│  Credentials in: /etc/ivy/config.yaml       │
│  Owned by: ivy:ivy, mode 0640               │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│              parser hosts                    │
│                                             │
│  leaf binary ──► vine gRPC endpoint         │
│                  (mTLS client cert)          │
│                                             │
│  No database credentials                    │
│  No API keys                                │
│  No production write access                 │
│                                             │
│  Certs in: /etc/ivy-leaf/certs/             │
│  Owned by: ivy-leaf:ivy-leaf, mode 0600     │
└─────────────────────────────────────────────┘
```

### Key Principle

The leaf daemon has **zero credentials** except its mTLS client certificate. It cannot access the database, the LLM API, ClickUp, or any production service. It can only read files in allowed directories and report back to vine.

---

## Deployment Security

### Ansible Playbooks

- **Idempotent**: safe to run repeatedly
- **Auditable**: human-readable YAML, no shell commands where possible
- **Principle of least privilege**: users created with minimal permissions

### File Permissions

| File                        | Owner             | Mode | Purpose   |
| --------------------------- | ----------------- | ---- | --------- |
| `/usr/local/bin/ivy-leaf`   | root:root         | 0755 | Binary    |
| `/etc/ivy-leaf/config.yaml` | ivy-leaf:ivy-leaf | 0640 | Config    |
| `/etc/ivy-leaf/certs/*`     | ivy-leaf:ivy-leaf | 0600 | TLS certs |
| `/var/log/ivy-leaf/`        | ivy-leaf:ivy-leaf | 0750 | Logs      |

### systemd Hardening (leaf)

```
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/log/ivy-leaf /var/lib/ivy-leaf
```

---

## Known Limitations

1. **No container escape protection**: A determined attacker with a kernel exploit can escape Docker containers. Defense-in-depth (AppArmor, seccomp) reduces but doesn't eliminate this risk.

2. **LLM prompt injection**: The agent executes LLM-suggested commands in sandboxes only. Production parser hosts are never modified by the agent. However, a crafted input could trick the agent into running unintended commands within the sandbox.

3. **No mTLS yet**: The current implementation uses insecure gRPC connections. mTLS is planned and the infrastructure (cert paths, config fields) is in place.

4. **Single-organization**: No multi-tenant isolation. All sessions share the same vine instance.

5. **No file upload validation**: When ClickUp file uploads are implemented, files will need virus scanning and content validation before being placed in sandboxes.

6. **Docker socket access**: vine needs access to the Docker socket, which is equivalent to root access on the host. This is mitigated by running vine as a dedicated user in the `docker` group.

---

## Security Checklist

- [x] Leaf runs as dedicated system user with no login shell
- [x] Leaf only executes whitelisted commands with strict flag filtering
- [x] Path validation blocks traversal and symlink escapes
- [x] Agent sandboxes have no network access
- [x] Pipeline sandboxes are isolated on dedicated Docker networks
- [x] All output size-limited to prevent resource exhaustion
- [x] Command timeouts enforced
- [ ] mTLS enabled on gRPC connections
- [ ] AppArmor profile deployed and tested
- [ ] Secrets rotation procedure documented
- [ ] Audit log retention policy defined
- [ ] Intrusion detection monitoring on parser hosts
