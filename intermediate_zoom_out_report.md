# Goldfish Review Report: Stage 8 Intermediate Zoom Out Review

**Target:** `docs/designs/engram.md` (Lines 1-460)

## Reviewer Status

| Reviewer | Status |
| --- | --- |
| `egm-zoom-out` | completed |

## Summary of Findings

- 🔴 **Blockers:** 3
- 🟡 **Major:** 5
- 🟢 **Nits:** 3

---

## Prioritized Findings (Blockers -> Major -> Nits)

### 1. Accidental Complexity in Git-Like DAG Branching vs. Optimistic Concurrency Control (OCC) - Severity: Blocker
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 29-30, 53-57, 130-149, 176-197, 285-307, 442-443
* **Discrepancy Description**:
  In response to concurrent write collisions (Sequence 2), the specification attempts to store divergent revisions (`is_conflict: true`) inside `engram_revisions` without updating `head_rev`. Previous reviews identified that this creates primary key collisions on `(project_id, key, rev)` and orphaned branches, urging the introduction of Git-style DAG branch modeling (`parent_hashes TEXT[]`, branch tips, `resolve_conflict` tools). From a high-altitude architectural perspective, attempting to implement a multi-parent DAG merge engine in PostgreSQL directly contradicts Non-Goal NG2 ("Direct Git Automation is out of scope") and burdens a lightweight operational state plane with enormous accidental complexity. AI agents operating on memory invariants do not need Git branches inside a database table; they need standard Optimistic Concurrency Control (OCC).
* **Rationale & Recommendation**:
  Prune the speculative DAG merge engine in favor of strict, standard Optimistic Concurrency Control:
  1. When an agent submits `store_memory` with an `expected_parent_hash` that does not match `engram_memories.current_hash`, the daemon rejects the write with HTTP `409 Conflict` (`ERR_STALE_REVISION`).
  2. The rejection payload returns the current HEAD hash, HEAD content, and a unified diff between the proposal and current HEAD.
  3. The calling agent reconciles the divergence in its own context window and submits a fresh write targeting the updated HEAD.
  4. If audit logging of rejected attempts is desired, write the attempt to an append-only audit table (`engram_mutation_rejections`), keeping `engram_revisions` strictly linear, monotonic, and free from branch forks or PK collisions. This eliminates the need for `parent_hashes TEXT[]`, merge commits, or a dedicated `resolve_conflict` tool.

---

### 2. Fragile Ephemeral LISTEN/NOTIFY and 8KB Payload Ceiling vs. Transactional Outbox Pattern - Severity: Blocker
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 11, 78-89, 106-108, 340-348, 401-403
* **Discrepancy Description**:
  The architecture relies on PostgreSQL PL/pgSQL triggers firing `NOTIFY engram_events, payload` bridged to in-memory Go channels for SSE streaming (`GET /sse`). This violates two production invariants:
  1. PostgreSQL enforces a strict 8,000-byte limit on `NOTIFY` payload strings. Any memory update, code guideline, or diff exceeding 8KB causes the database trigger to raise a fatal error (`payload string too long`), aborting the mutating transaction (`store_memory`, `complete_task`).
  2. PostgreSQL `NOTIFY` is completely ephemeral. Network jitter, Tailscale route handshakes, or transient client reconnections result in permanent event loss because there is no monotonic sequence, persistent replay buffer, or `Last-Event-ID` offset recovery.
* **Rationale & Recommendation**:
  Replace the trigger-based `NOTIFY` mechanism with a durable Transactional Outbox Pattern (standardized in the team's monorepo `auk` architecture):
  1. Add a dedicated, durable outbox table:
     ```sql
     CREATE TABLE engram_events (
         id BIGSERIAL PRIMARY KEY,
         project_id TEXT NOT NULL REFERENCES engram_projects(id) ON DELETE CASCADE,
         topic TEXT NOT NULL,
         payload JSONB NOT NULL,
         created_at TIMESTAMPTZ NOT NULL DEFAULT now()
     );
     CREATE INDEX idx_engram_events_project_id ON engram_events(project_id, id ASC);
     ```
  2. In mutating Go services (`store_memory`, `claim_task`), insert the event record within the same `pgx.Tx` transaction and execute a zero-payload wake-up ping (`NOTIFY engram_events, 'ping'`). This eliminates PL/pgSQL triggers entirely, bypasses the 8KB limit, and ensures events are never lost if the daemon restarts.
  3. The background listener worker receives the ping, queries `SELECT * FROM engram_events WHERE id > $last_seen_id ORDER BY id ASC`, and broadcasts events to in-memory SSE subscriber channels.
  4. SSE endpoints support standard `Last-Event-ID: N` headers, streaming all missed events `WHERE id > N` upon reconnection to guarantee at-least-once delivery across flaky network boundaries.

---

### 3. Split-Brain Execution via Unfenced Task Mutations & Poison-Pill Starvation Loops - Severity: Blocker
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 31-32, 58-62, 218-240, 309-338, 412-415, 429-430
* **Discrepancy Description**:
  The task state engine (Section 3, Sequence 3) contains two critical operational failure modes:
  1. Lack of Lease Fencing: Task mutation endpoints (`complete_task`, `renew_task_lease`, `fail_task`) do not validate the worker's `lease_id`. If Worker A experiences a network delay, OS freeze, or GC pause exceeding its lease TTL, and Worker B subsequently steals the task via `SKIP LOCKED`, Worker A can wake up and invoke `complete_task`, overwriting Worker B's state and inducing split-brain task corruption.
  2. Poison-Pill Worker Starvation: While `claim_count` is incremented on each steal, there is no maximum retry threshold or terminal state guard. If a task contains a deterministic crash bug or unparseable payload, consecutive workers will claim, crash, expire, and re-claim the task indefinitely, crashing the agent swarm. Furthermore, Sequence 3 separates expired lease claiming and tombstone insertion into two separate application steps rather than an atomic database transaction.
* **Rationale & Recommendation**:
  Harden the task state plane with three targeted primitives:
  1. Enforce strict lease fencing tokens: Every task mutation MUST include `WHERE project_id = $1 AND id = $2 AND lease_id = $3`. If 0 rows are updated, return HTTP `409 Conflict` (`ERR_LEASE_LOST`), instantly terminating zombie worker modifications.
  2. Combine lease stealing, autopsy tombstone insertion, and poison-pill guards into a single atomic Common Table Expression (CTE):
     ```sql
     WITH target_task AS (
         SELECT id, claim_count FROM engram_tasks
         WHERE project_id = $1 AND queue = $2
           AND (status = 'PENDING' OR (status = 'CLAIMED' AND lease_expires_at < now()))
         ORDER BY priority DESC, created_at ASC
         LIMIT 1
         FOR UPDATE SKIP LOCKED
     ), updated AS (
         UPDATE engram_tasks t
         SET status = CASE WHEN target_task.claim_count >= 3 THEN 'FAILED' ELSE 'CLAIMED' END,
             claimed_by = CASE WHEN target_task.claim_count >= 3 THEN NULL ELSE $worker END,
             lease_id = CASE WHEN target_task.claim_count >= 3 THEN NULL ELSE $new_lease_id END,
             lease_expires_at = CASE WHEN target_task.claim_count >= 3 THEN NULL ELSE now() + $duration END,
             claim_count = t.claim_count + 1,
             updated_at = now()
         FROM target_task
         WHERE t.id = target_task.id
         RETURNING t.*
     ), tombstone AS (
         INSERT INTO engram_task_handoffs (project_id, task_id, from_agent, to_agent, verdict, reason)
         SELECT u.project_id, u.id, 'system/lease-reaper', coalesce(u.claimed_by, 'none'),
                CASE WHEN u.status = 'FAILED' THEN 'FAIL' ELSE 'LEASE_EXPIRED' END,
                CASE WHEN u.status = 'FAILED' THEN 'Task exceeded max claim attempts (3); marked FAILED'
                     ELSE 'Lease expired without heartbeat' END
         FROM updated u WHERE u.claim_count > 1
     )
     SELECT * FROM updated;
     ```
  3. Add `max_retries INT NOT NULL DEFAULT 3` to `engram_tasks` and update DDL accordingly.

---

### 4. Elimination of Revision Squashing to Preserve True Cryptographic Immutability (YAGNI) - Severity: Major
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 29, 85, 188-189, 411, 426
* **Discrepancy Description**:
  Lines 188-189 introduce squashing columns (`is_squashed BOOLEAN`, `squashed_into_rev INT`), and Stage 4 defines a `squash_revisions` MCP tool. Previous reviews noted that squashing destroys the linear parent-hash chain, proposing complex mitigations such as squash boundary tokens, parent hash arrays, and custom DAG traversals. However, revision squashing is an unnecessary holdover from Git feature-branch workflows that directly contradicts Engram's core identity (Goal G2: "Cryptographic Blame & Provenance: Every memory write is append-only... unbroken SHA-256 parent hash chain"). Memory entries are small text artifacts (<50KB) compressed natively by PostgreSQL TOAST; squashing saves negligible storage while introducing substantial algorithmic risk, potential hash invalidation, and cognitive overhead.
* **Rationale & Recommendation**:
  Apply strict YAGNI: Remove revision squashing entirely:
  1. Remove `is_squashed` and `squashed_into_rev` from `engram_revisions`.
  2. Remove `squash_revisions` from SQL queries, domain services, MCP tools, and CLI commands.
  3. Retain the revision log as a strictly append-only, immutable linear ledger where every revision cryptographically verifies `parent_hash == prev.hash`. This simplifies the implementation by dozens of lines of code and eliminates provenance edge cases.

---

### 5. Omission of Full Content Persistence in `engram_revisions` Breaks Historical Hydration - Severity: Major
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 176-195, 298-306, 424-426
* **Discrepancy Description**:
  The `engram_revisions` table schema stores only `diff TEXT NOT NULL DEFAULT ''`, omitting full memory content (`content`). Full text exists solely in `engram_memories.content` for the latest HEAD revision. This means historical revisions cannot be inspected, restored, or verified without recursively applying unified diffs backwards from HEAD across all prior revisions. If any diff fails to patch cleanly or contains whitespace discrepancies, historical content reconstruction fails completely. Furthermore, revision 1 has no previous revision to diff against, leaving its initial representation ambiguous.
* **Rationale & Recommendation**:
  Add `content TEXT NOT NULL` directly to `engram_revisions`:
  ```sql
  CREATE TABLE engram_revisions (
      project_id TEXT NOT NULL,
      key TEXT NOT NULL,
      rev INT NOT NULL,
      hash TEXT NOT NULL,
      parent_hash TEXT NOT NULL,
      author TEXT NOT NULL,
      agent_model TEXT NOT NULL,
      machine_id TEXT NOT NULL,
      content TEXT NOT NULL,
      diff TEXT NOT NULL DEFAULT '',
      rationale TEXT NOT NULL DEFAULT '',
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      PRIMARY KEY (project_id, key, rev),
      CONSTRAINT fk_revision_memory FOREIGN KEY (project_id, key)
          REFERENCES engram_memories(project_id, key) ON DELETE CASCADE
  );
  ```
  At the data volume of architectural guidelines and rules, storing snapshot content per revision consumes negligible disk space (due to PostgreSQL TOAST lz4/pglz compression) and makes historical retrieval, diff recalculation, and hash verification $O(1)$ and deterministic.

---

### 6. Omission of Essential Operational MCP Tools for Task Queueing and Knowledge Search - Severity: Major
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 442-444 (`internal/mcp/tools.go`)
* **Discrepancy Description**:
  Line 443 lists the registered MCP tools: `list_manifest`, `get_memories_batch`, `store_memory`, `blame_memory`, `squash_revisions`, `claim_task`, `renew_task_lease`, `complete_task`, and `log_episode`. Multiple foundational operations described in the design and required by agents are completely missing:
  1. Task Enqueuing: There is no `create_task` tool. Agents or orchestrators cannot insert work into the state plane over MCP.
  2. Task Failure Reporting: `engram_tasks` allows statuses `'FAILED'` and `'BLOCKED'`, but the MCP toolset only exposes `complete_task`. An agent that encounters an unrecoverable error cannot mark a task failed or record a handoff.
  3. Task Handoff Recording: `handoff.go` defines a strict validation contract (<150 words, verdict, evidence), but no MCP tool (`record_handoff`) exists to expose this contract to agents.
  4. Knowledge Search: While Stage 2 writes queries for hybrid and fulltext search, no MCP search tool (`search_memories`, `search_episodes`) is exposed to agents.
* **Rationale & Recommendation**:
  Update `internal/mcp/tools.go` and `cmd/engram` to include the complete operational toolset:
  - `create_task(project_id, queue, title, payload, priority)`
  - `fail_task(project_id, task_id, lease_id, reason, details)`
  - `record_handoff(project_id, task_id, to_agent, verdict, reason, metadata)`
  - `search_memories(project_id, query, scope, limit)`
  Remove `squash_revisions` per Finding 4.

---

### 7. Pragmatic Embedding Strategy vs. Dynamic Runtime DDL Speculation - Severity: Major
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 112-113, 156-174
* **Discrepancy Description**:
  Line 112 claims the schema was partitioned "To eliminate vector dimension rigidity", yet lines 162-173 hardcode fixed columns `embedding_1536` and `embedding_768`. Previous reviews recommended implementing dynamic runtime DDL helpers or table partitioning per model. From a systems stability standpoint, having a Go daemon dynamically execute `CREATE TABLE` or `ALTER TABLE ADD COLUMN` in production databases introduces severe locking, permission, and migration consistency hazards.
* **Rationale & Recommendation**:
  Adopt a clean, pragmatic vector stance:
  1. Eliminate the inaccurate claim of "eliminating vector dimension rigidity" in line 112.
  2. Explicitly document that Engram supports a dual-tier standard dimension profile: 768-d for local/compact models (e.g., `nomic-embed-text`, `bge-base`) and 1536-d for cloud models (e.g., OpenAI `text-embedding-3-small`).
  3. Reiterate Goal G6: Vector search is an optional semantic accelerator; deterministic PostgreSQL fulltext search (`tsvector` with `GIN` and trigram matching) is the primary, zero-dependency retrieval engine. If an agent operates without embeddings, fulltext search provides 100% functionality with zero operational burden.

---

### 8. Over-Engineered Ephemeral SSE Ticket Handshake for Private Mesh Topology - Severity: Major
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 79, 437, 458
* **Discrepancy Description**:
  Critic review recommended introducing an ephemeral ticket handshake service (`POST /api/v1/sse/ticket` -> `GET /sse?ticket=...`) to avoid passing Bearer tokens in SSE query parameters. However, Non-Goal NG3 explicitly scopes Engram as a private, self-contained daemon operating exclusively over localhost or private Tailscale mesh networks. Standard MCP Streamable HTTP / SSE implementations using modern client libraries (`fetch`-based EventSource) support standard HTTP `Authorization: Bearer <token>` headers directly on the SSE request. Creating a stateful in-memory ticket cache with TTL expiration and cleanup routines introduces unnecessary moving parts and failure modes.
* **Rationale & Recommendation**:
  Standardize on HTTP `Authorization: Bearer <token>` headers as the canonical authentication mechanism for all endpoints, including `GET /sse`. Retain `?token=` strictly as an optional fallback for developer terminal debugging via `curl`, accompanied by a clear warning that query parameters are logged in HTTP server access logs. Reject the ephemeral ticket broker proposal as excessive for internal mesh deployments.

---

### 9. Duplicate and Incompatible Fiber v3 Dependency in `go.mod` - Severity: Nit
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 8-9 in `go.mod`, Line 80 in `engram.md`
* **Discrepancy Description**:
  `go.mod` currently imports both `github.com/gofiber/fiber/v2 v2.52.16` and `github.com/gofiber/fiber/v3 v3.7.0`. Fiber v3 is a major, breaking rewrite that is still in development, whereas Huma v2 provides mature and tested integration for Fiber v2 (`huma/adapters/humafiber`).
* **Rationale & Recommendation**:
  Remove `github.com/gofiber/fiber/v3` from `go.mod` and standardize exclusively on Fiber v2 across the entire codebase.

---

### 10. Inaccurate Token Budget Estimation via Word Segmentation (`uax29`) - Severity: Nit
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 28, 139, 426
* **Discrepancy Description**:
  Line 426 specifies "token estimation via `uax29`" (`github.com/clipperhouse/uax29/v2`). UAX #29 standardizes natural language word segmentation, not LLM Byte-Pair Encoding (BPE). In technical text, snake_case/camelCase variables, and markdown diffs, UAX #29 underestimates BPE token counts by 30% to 50%, undermining Goal G1's token budget guarantee (<1,500 tokens across 100 manifest entries).
* **Rationale & Recommendation**:
  Replace `uax29` with a lightweight Go BPE tokenizer (e.g., `tiktoken-go` with `cl100k_base` encoding) or an explicit calibrated character heuristic (e.g., `ceil(len(text) / 3.7)`) to ensure accurate token budgeting in manifests.

---

### 11. Semantic Contradiction in Autopsy Tombstone Verdict - Severity: Nit
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 61, 248, 336, 428
* **Discrepancy Description**:
  When a worker steals an expired task, the reaper records a tombstone in `engram_task_handoffs` with `verdict: "BLOCKED"`. However, the task is immediately returned to the new worker with `status: "CLAIMED"`. Marking an active, claimed task's latest handoff as "BLOCKED" creates confusion in audit logs and violates the handoff contract where BLOCKED implies waiting on external blockers.
* **Rationale & Recommendation**:
  Add `LEASE_EXPIRED` to the allowed handoff verdicts: `CHECK (verdict IN ('PASS', 'FAIL', 'BLOCKED', 'NEEDS_REVIEW', 'HANDOFF', 'LEASE_EXPIRED'))`. When an expired lease is stolen, record `verdict: 'LEASE_EXPIRED'`. If the task exceeds `max_retries`, record `verdict: 'FAIL'`.
