---
name: engram
description: High-density state and memory plane workflow for AI agents and engineers — progressive disclosure, atomic task claiming (SKIP LOCKED), lease fencing, immutable cross-agent handoffs, and cryptographic blame. Use whenever interacting with Engram (CLI or MCP), querying or updating project architecture/rules, claiming operational tasks, recording handoffs, or managing the lifecycle between runtime state in Engram and canonical documentation in Git (docs/, ADRs, TODO.md). Trigger on mentions of 'engram', 'memoria', 'state plane', 'task claim', 'lease', 'handoff', 'blame', 'manifest', o sincronización de documentación y TODOs.
---

# Engram — State & Memory Plane Workflow

Engram is a high-density, transactional state and memory plane backed by PostgreSQL 18 (`pgvector`) and exposed via MCP / JSON-RPC / CLI. It eliminates context window saturation, multi-agent race conditions in `TODO.md`, and unverified handoffs.

---

## 1. The Dual-Plane Architecture

Engineering state is split into two distinct, complementary planes:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                   GIT REPOSITORY (CANONICAL CANON)                     │
│  - Human-facing architecture docs (${ENGRAM_DOCS_DIR:-docs/}, ADRs)   │
│  - Static source of truth, disaster recovery, cold-start bootstrap     │
│  - Code review in GitHub PRs                                           │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ Derivación Semántica & Late Sync
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                   ENGRAM STATE PLANE (RUNTIME PLANE)                   │
│  - Progressive disclosure: 1k token manifests vs 21k token file dumps  │
│  - Atomic task queues & row-level lease fencing (SKIP LOCKED)          │
│  - Immutable handoffs with reproducible evidence & artifact hashes     │
│  - Cryptographic blame (SHA-256 chain, exact LLM model, machine ID)    │
└────────────────────────────────────────────────────────────────────────┘
```

### Core Directives
1. **Never dump files into context:** Do not call `read_file` on entire documentation directories. Query Engram's skeletal manifest first.
2. **Never edit `TODO.md` or docs in flight:** While implementing a feature or task, keep Git working trees 100% clean. Task claiming and status in progress live exclusively in Engram.
3. **Late Consolidation in Git:** Documentation and `TODO.md` checkmarks are updated **once at the end of the task**, inside the exact same commit that delivers the tested code and passes verification.
4. **Offline Resilience Fallback:** If Engram daemon is unreachable and cannot be started via Docker, fall back gracefully to Local Filesystem Mode (see Section 2.3).

---

## 2. Server Resolution, Bootstrap & Fallback Policy

Engram operates as a central shared service with local container/daemon support and a strict offline filesystem fallback.

| Variable | Default (Local) | Remote / Central Production | Description |
| :--- | :--- | :--- | :--- |
| `ENGRAM_SERVER_URL` | `http://localhost:8085` | `https://engram.example.com` o `http://<remote-server-ip>:8085` | Endpoint del demonio o servidor Engram |
| `ENGRAM_AUTH_TOKEN` | `engram_dev_token_2026` | Token central aprovisionado en el servidor | Bearer token de autenticación |
| `ENGRAM_PROJECT_ID` | Basename del repo git (ej. `engram`) | Nombre del proyecto en el servidor | Identificador de aislamiento de memoria y tareas |
| `ENGRAM_DOCS_DIR` | Auto-detectado (`docs/`, `doc/`, `./`) | Directorio canónico de documentación | Ubicación de la documentación canónica en Git |

### 2.1 Project & Documentation Resolution
When operating within a repository:
1. **Project Identifier (`ENGRAM_PROJECT_ID`):** Defaults to `$(basename $(git rev-parse --show-toplevel 2>/dev/null || pwd))`. Can be overridden via environment variable or per-command flag.
2. **Docs Directory (`ENGRAM_DOCS_DIR`):** Configurable documentation root. If unset, auto-discovers in order: `docs/`, `documentation/`, `<project_id>-docs/`, or repository root (`./`).
3. **Tasks Markdown (`ENGRAM_TODO_FILE`):** Path to tasks checklist; defaults to `${ENGRAM_DOCS_DIR}/TODO.md` or `./TODO.md`.

### 2.2 Connectivity Probe
Before executing state plane operations, test endpoint health:
```bash
curl -s -m 1.5 "${ENGRAM_SERVER_URL:-http://localhost:8085}/healthz" || echo "ENGRAM_UNAVAILABLE"
```

### 2.3 Local Container Stack Bootstrap
If the probe returns `ENGRAM_UNAVAILABLE` on a local development machine and Docker is accessible, the agent can boot the stack using the official images:

1. **Locate repository or docker-compose:**
   The canonical stack resides at `docker-compose.yml` in the Engram repository:
   ```bash
   docker compose -f <path-to-engram>/docker-compose.yml up -d
   ```
2. **Components in Docker:**
   - `engram-daemon`: Container running `ghcr.io/ivangsm/engram:latest` on port `8085`.
   - `engram-postgres`: Container running `pgvector/pgvector:pg18` on `127.0.0.1:5439` (mapped from internal 5432, overridable via `ENGRAM_DB_PORT`).
3. **Wait for Health Probe:**
   ```bash
   until curl -sf http://127.0.0.1:8085/healthz > /dev/null 2>&1; do sleep 1; done
   ```
4. Once verified healthy, resume standard 4-Phase MCP/CLI operations.

### 2.4 Contingency Fallback: Local Filesystem Mode (Zero-Engram Policy)
If Engram is unreachable AND cannot be booted (e.g. Docker unavailable, port conflicts, sandboxed CI runner, offline environment):
- **NEVER BLOCK OR HALT DEVELOPMENT:** The agent MUST continue work seamlessly in degraded local mode.
- **NEVER HALLUCINATE ENGRAM RESPONSES:** Do not invent mock `lease_id` tokens or fake memory hashes.
- **Targeted Local Reads:**
  - Read only specific target markdown files (`${ENGRAM_TODO_FILE:-TODO.md}`, `${ENGRAM_DOCS_DIR:-docs/}`, `.serena/memories/`, or component `README.md` contracts).
  - Strictly observe token frugality: use line-range reads (`read_file` with `StartLine`/`EndLine`) instead of dumping full directories.
- **Local Task State & Checkmarking:**
  - Track in-flight tasks in memory during the session.
  - Apply task transitions directly to `TODO.md` (`- [x]`) upon completion.
- **Transparency Notification:**
  - Inform the user in the initial turn:
    > "Nota: Engram no está disponible y no pudo ser iniciado en Docker. Operando en modo degradado local sobre archivos Markdown en disco."

---

## 3. Four-Phase Agent Execution Protocol

Every task assigned to an agent must strictly follow this 4-phase sequence:

### Phase 1: Progressive Discovery & Task Claiming
1. **Inspect Manifest (Zero Plaintext Dump):**
   Call `list_memory_manifest(project_id="<project-id>")`.
   - Returns keys, scopes, token counts, and single-line digests (<20 words).
   - Consumes ~1,000 tokens instead of 20,000+ tokens.
2. **Claim Atomic Lease:**
   Call `claim_task(project_id="<project-id>", task_id="TASK-ID", leased_by="agent-name", ttl_seconds=300)`.
   - Uses PostgreSQL `FOR UPDATE SKIP LOCKED`.
   - Returns a cryptographically random `lease_id` UUID token.
   - If another worker holds the task, the call fails cleanly. Race conditions are mathematically prevented.

### Phase 2: Targeted Hydration & In-Flight Isolation
1. **Hydrate Only What Is Needed:**
   Call `recall_memory(project_id="<project-id>", key="key-name")` only for the specific contracts required (e.g. `auth-identity-model`, `db-schema-invariants`).
2. **Implement Code and Tests:**
   Work in the target service repository.
   - **STRICT PROHIBITION:** Do NOT modify `TODO.md` or architecture markdown files while coding.
   - If the task runs longer than expected, extend the lease:
     `renew_task_lease(project_id, task_id, lease_id, extend_seconds=300)`.

### Phase 3: Runtime Verification & Immutable Handoff
1. **Execute Tests:** Run real tests against real environments (`go test -race`, `vitest run`).
2. **Record Handoff:**
   Call `record_handoff(...)`:
   - `verdict`: `PASS` | `FAIL` | `BLOCKED` | `NEEDS_REVIEW`
   - `summary`: Strictly <150 words executive summary.
   - `reproducible_evidence`: Shell commands, test pass counts, terminal proofs.
   - `artifacts`: Serialized JSON array of changed file paths.

### Phase 4: Late Consolidation & Git Commit
Only after obtaining a verified `PASS` handoff:
1. **Update Memory in Engram (Strict OCC):**
   If an architectural invariant changed, call `store_memory` providing `expected_parent_hash`. If a collision occurs, Engram returns `ERR_STALE_REVISION` with a unified diff for automated semantic 3-way merge.
2. **Update Git Documentation & TODO:**
   - Mark the completed item in `TODO.md` (`- [x]`).
   - Update any relevant technical markdown in `docs/` or component `README.md` files.
3. **Complete Task in State Plane:**
   Call `complete_task(project_id, task_id, lease_id)`.
4. **Atomic Git Commit:**
   Commit code + documentation + `TODO.md` in a single conventional commit on the target branch (or PR).

---

## 4. MCP Tools & CLI Reference

Engram is accessible via JSON-RPC over HTTP (`POST /mcp/message` with Bearer auth) or through the `engram` CLI:

### 4.1 Memory Tools
- **`list_memory_manifest(project_id, scope?)`**  
  *CLI:* `engram manifest <project_id> [--scope <scope>]`  
  Returns array of `{ key, scope, digest, token_count, latest_revision_id, updated_at }`.
- **`recall_memory(project_id, key)`**  
  *CLI:* `engram recall <project_id> <key>`  
  Returns full `{ id, key, scope, digest, content, token_count, created_at, updated_at }`.
- **`search_memory(project_id, query, limit?)`**  
  *CLI:* `engram search <project_id> <query> [--limit <n>]`  
  Hybrid rank (cosine vector distance 0.65 + PostgreSQL `ts_rank` 0.35).
- **`store_memory(project_id, key, content, rationale, digest?, scope?, expected_parent_hash?, author_user?, author_agent_model?, machine_id?)`**  
  *CLI:* `engram store <project_id> <key> <content_or_file> --rationale <reason> [--digest <summary>] [--expected-parent-hash <sha256> | --occ] [--author <user>] [--model <model_id>]`  
  Appends to audit log with SHA-256 hash chaining (omitting `--digest` on updates preserves the prior curated digest).
- **`blame_memory(project_id, key)`**  
  *CLI:* `engram blame <project_id> <key>`  
  Returns cryptographic genealogy, author, machine, model, rationale, and unified diffs.

### 4.2 State Plane & Task Tools
- **`create_task(project_id, task_id, title, description, priority, metadata?)`**  
  *CLI:* `engram task create <project_id> <task_id> <title> [--desc <text>] [--priority <n>]`  
  Enqueues an operational task with integer priority.
- **`list_tasks(project_id, status?, limit?)`**  
  *CLI:* `engram task ls <project_id> [--status <status>] [--limit <n>] [--json]`  
  Lists operational tasks for a project.
- **`get_task(project_id, task_id)`**  
  *CLI:* `engram task get <project_id> <task_id>`  
  Returns detailed state and active lease metadata for a task.
- **`claim_task(project_id, task_id?, leased_by?, ttl_seconds?)`**  
  *CLI:* `engram task claim <project_id> [task_id] [--leased-by <id>] [--ttl <seconds>]` (alias: `engram claim ...`)  
  Acquires task lease via `FOR UPDATE SKIP LOCKED`.
- **`renew_task_lease(project_id, task_id, lease_id, extend_seconds)`**  
  *CLI:* `engram task renew <project_id> <task_id> --lease <lease_id> [--ttl <seconds>]`  
  Fenced lease renewal; fails with `ERR_LEASE_LOST` if `lease_id` does not match active lease.
- **`record_handoff(project_id, task_id, from_agent, to_agent?, verdict, summary, reproducible_evidence, artifacts?, complete_task?, lease_id?)`**  
  *CLI:* `engram handoff <project_id> <task_id> <verdict> <summary> <evidence> [--from-agent <id>] [--complete --lease <lease_id>]`  
  Records immutable handoff record (and optionally completes the task atomically with `--complete`).
- **`complete_task(project_id, task_id, lease_id?)`**  
  *CLI:* `engram task complete <project_id> <task_id> [--lease <lease_id>]`  
  Marks task `completed` and releases locks.
- **`fail_task(project_id, task_id, lease_id?, reason)`**  
  *CLI:* `engram task fail <project_id> <task_id> [--lease <lease_id>] [--reason <text>]`  
  Marks task `failed` with recorded autopsy reason.

---

## 5. Invariant & Memory Modeling Best Practices

1. **High-Density Digest:** Exactly 1 line, under 20 words. No filler words ("Este documento explica..."). Start directly with the invariant.
   - *Good:* `Tokens OTP efímeros con rate limit; cero contraseñas en API.`
   - *Bad:* `Documento que describe como funciona la autenticacion de los usuarios y que librerias usamos.`
2. **Atomic Invariants:** One key represents exactly one contract or boundary rule.
   - *Good keys:* `auth-identity-model`, `multi-tenant-isolation`, `append-only-time-ledger`, `db-open-interval-constraint`.
3. **Mandatory Rationale:** Never store a memory without technical justification explaining the trade-off.
4. **Optimistic Concurrency Control:** When updating an existing key, always read `current_hash` first and pass it as `expected_parent_hash`. Never do blind overwrites.
5. **No AI Attribution in Git:** Machine IDs and model identifiers are logged strictly inside Engram's blame table, never leaked into Git commit trailers.
