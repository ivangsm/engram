# Goldfish Review Report: Stage 5 Initial Zoom Out Review

**Target:** `docs/designs/engram.md` (Lines 1-460)

## Reviewer Status

| Reviewer | Status |
| --- | --- |
| `egm-zoom-out` | completed |
| `egm-inventive-catalyst` | completed |

## Summary of Findings

- 🔴 **Blockers:** 3
- 🟡 **Major:** 4
- 🟢 **Nits:** 3

---

## Prioritized Findings (Blockers -> Major -> Nits)

### 1. Ephemeral SSE Event Streaming & Network Flap Desynchronization - Severity: Blocker
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 11, 78-88, 106-108, 341-348
* **Discrepancy Description**:
  Engram specifies real-time state coordination across heterogeneous nodes (local workstations, remote VPS, CI runners) using PostgreSQL `LISTEN/NOTIFY` bridged to in-memory Go channels (`chan []byte`) for SSE streaming (`GET /sse`). PostgreSQL `NOTIFY` is strictly ephemeral and connection-scoped. If a network partition, Tailscale latency spike, or client disconnection occurs, notifications published during that window are permanently lost. The design lacks an event replay offset, monotonic stream sequence, or support for the standard `Last-Event-ID` header upon client reconnection.
* **Rationale & Recommendation**:
  In-memory event distribution without persistent replay violates the core invariant of multi-machine state coordination. Transition from transient `LISTEN/NOTIFY` payloads to a durable outbox/event log using `engram_episodes` or a dedicated `engram_event_stream` table keyed by a monotonic `BIGSERIAL` sequence (`event_id`). Use PostgreSQL `NOTIFY` solely as a zero-payload wake-up ping carrying the latest `event_id`. When SSE clients connect or reconnect with `Last-Event-ID: N`, the server serves all events `WHERE event_id > N ORDER BY event_id ASC`. This guarantees delivery across network flaps and complies with the SSE specification.

---

### 2. Atomic Lease Stealing Concurrency Race & Split-Brain Execution - Severity: Blocker
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 31-32, 58-62, 226-235, 309-338, 412-415, 429-430
* **Discrepancy Description**:
  Sequence 3 (lines 333-337) and Stage 3 services split expired lease stealing and diagnostic tombstone creation into two separate application steps: an atomic steal query followed by an independent `INSERT INTO engram_task_handoffs`. If the daemon fails between these operations, the task is claimed without an autopsy record, compromising auditability. Furthermore, the task mutation model does not enforce lease fencing tokens (`lease_id` validation on state transitions). If a worker experiences a long GC pause, process freeze, or network delay and resumes after its lease has been stolen by another agent, it can invoke `complete_task` or `renew_task_lease` and silently corrupt the task state.
* **Rationale & Recommendation**:
  1. Combine task acquisition and tombstone recording into a single atomic Common Table Expression (CTE) query:
     ```sql
     WITH stolen AS (
         UPDATE engram_tasks
         SET status = 'CLAIMED', claimed_by = $worker, lease_id = $new_lease_id,
             lease_expires_at = now() + $duration, claim_count = claim_count + 1,
             updated_at = now()
         WHERE (project_id, id) IN (
             SELECT project_id, id FROM engram_tasks
             WHERE project_id = $1 AND queue = $2
               AND (status = 'PENDING' OR (status = 'CLAIMED' AND lease_expires_at < now()))
             ORDER BY priority DESC, created_at ASC
             LIMIT 1
             FOR UPDATE SKIP LOCKED
         )
         RETURNING *
     ), tombstone AS (
         INSERT INTO engram_task_handoffs (project_id, task_id, from_agent, to_agent, verdict, reason)
         SELECT s.project_id, s.id, 'system/lease-reaper', $worker, 'BLOCKED', 'Lease expired without heartbeat'
         FROM stolen s WHERE s.claim_count > 1
     )
     SELECT * FROM stolen;
     ```
  2. Enforce strict lease fencing: every task mutation (`complete_task`, `renew_task_lease`) MUST execute `UPDATE engram_tasks ... WHERE project_id = $1 AND id = $2 AND lease_id = $3`. If 0 rows are affected, the operation must immediately return `409 Conflict` (`ERR_LEASE_LOST`), preventing zombie workers from modifying state.

---

### 3. Orphaned Conflict Branches & Missing Reconciliation Lifecycle - Severity: Blocker
* **Reviewer Lens:** `egm-inventive-catalyst`
* **Affected Scope:** Lines 53-57, 130-149, 176-197, 285-307, 442-443
* **Discrepancy Description**:
  Under optimistic mutation collisions, Engram appends a revision with `is_conflict = true` to `engram_revisions` and returns `status: "CONFLICT"` along with a unified diff. However, `engram_memories` remains locked to `head_rev` (the winning parent). The conflicting proposal becomes an orphaned branch in `engram_revisions`. The design defines no MCP tool or workflow to resolve conflicts (tools list in line 443 lacks `resolve_conflict`), and `list_manifest` provides no indication that unresolved conflicting revisions exist. Furthermore, `engram_revisions` defines `parent_hash TEXT` as a single scalar, preventing a resolved merge revision from referencing both branch heads in the cryptographic DAG.
* **Rationale & Recommendation**:
  1. Expose conflict state in the skeletal manifest: add `has_conflict BOOLEAN` (or `conflict_revs INT`) to `engram_memories` and `list_manifest` so agents discover unresolved divergent branches without manual history crawling.
  2. Define an explicit resolution endpoint in MCP tools: add `resolve_conflict(project_id, key, conflict_rev, resolved_content)` or permit `store_memory` to accept `resolved_conflict_rev`.
  3. Expand `engram_revisions` to record multi-parent DAG merges: replace or supplement `parent_hash TEXT` with `parent_hashes TEXT[]` (or `merge_parent_hash TEXT`) so merge commits cryptographically bind both divergent branches.

---

### 4. Rigid Vector Dimension Columns Contradicting DDL Goals - Severity: Major
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 113, 156-174, 394
* **Discrepancy Description**:
  Line 113 states the schema is structured "to eliminate vector dimension rigidity", yet lines 162-173 explicitly define fixed columns `embedding_1536 VECTOR(1536)` and `embedding_768 VECTOR(768)` with separate HNSW indexes. Hardcoding specific dimension widths in the core DDL prevents the deployment of models with alternative dimensions (e.g., 384 for `all-MiniLM-L6-v2`, 1024 for `bge-large`, or 3072 for OpenAI `text-embedding-3-large`) without requiring manual schema migrations.
* **Rationale & Recommendation**:
  Acknowledge `pgvector` constraints: vector dimensions are fixed per column type in PostgreSQL. To handle heterogeneous models cleanly:
  - Either partition embedding tables by dimension family (e.g., `engram_embeddings_768`, `engram_embeddings_1536`), or
  - Document the supported vector dimension profiles explicitly in the architectural invariants, accompanied by a dynamic DDL migration helper that registers additional dimension columns and partial HNSW indexes upon configuration.

---

### 5. Cryptographic Hash Chain Breakage During Revision Squashing - Severity: Major
* **Reviewer Lens:** `egm-inventive-catalyst`
* **Affected Scope:** Lines 29, 85, 188-189, 411, 426
* **Discrepancy Description**:
  G2 requires an unbroken cryptographic SHA-256 parent hash chain across all revisions. However, Stage 2 and Stage 3 introduce `squash_revisions` (`is_squashed = true`, `squashed_into_rev = $rev`). The document does not define the parent hash pointer or hashing semantics for a squashed node. If revisions $R_2$ through $R_5$ are squashed into $R_6$, pointing $R_6$'s `parent_hash` to $R_1$ invalidates linear traversal of $R_2$–$R_5$, whereas pointing to $R_5$ makes intermediate diffs unreconstructible without special handling.
* **Rationale & Recommendation**:
  Define a formal Squash Commit specification:
  1. A squashed revision must record `squashed_from_rev INT` and `squashed_to_rev INT`.
  2. Set `parent_hash` of the squashed revision to the parent of `squashed_from_rev`.
  3. Include a `squashed_hashes TEXT[]` array containing the hashes of all collapsed revisions, and incorporate `squashed_hashes` into the SHA-256 computation of the squashed revision.
  4. Ensure `blame_memory` verification algorithms recognize squashed ranges and validate both the collapsed boundary and historical checkpoints.

---

### 6. Unbounded Growth & Search Latency Degradation in Event and Revision Tables - Severity: Major
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 176-215, 416-417, 431-432
* **Discrepancy Description**:
  `engram_episodes` logs every agent narrative event with fulltext search vectors (`tsv`), and `engram_revisions` records every memory change and diff. In autonomous multi-agent environments with high prompt-loop cadence and CI runs, these tables accumulate hundreds of thousands of rows rapidly. The design lacks table partitioning, retention policies, compaction, or archiving strategies. Over time, GIN indexes (`idx_engram_episodes_tsv`) will outgrow RAM working sets, degrading search and write latency.
* **Rationale & Recommendation**:
  1. Implement PostgreSQL declarative range partitioning on `engram_episodes` by `created_at` (e.g., monthly partitions).
  2. Establish a clear retention lifecycle: operational episodes retained in active storage for 90 days, with automated summarization or cold archiving.
  3. Provide an administrative CLI command (`engram prune --before <date>`) to maintain bounded operational scale.

---

### 7. Inaccurate Context Budget Estimation via Word Segmentation (`uax29`) - Severity: Major
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 28, 139, 426
* **Discrepancy Description**:
  Line 426 specifies "token estimation via `uax29`" (`github.com/clipperhouse/uax29/v2`). UAX #29 standardizes Unicode word boundary segmentation, which fundamentally diverges from Byte Pair Encoding (BPE) subword tokenizers used by modern LLMs. In source code, architectural diffs, camelCase identifiers, and punctuation-dense text, UAX #29 word segmentation undercounts BPE tokens by 30% to 60%. As a consequence, `token_count` in `engram_memories` and `list_manifest` will significantly underestimate true prompt consumption, breaking G1's guarantee (<1,500 tokens across 100 entries).
* **Rationale & Recommendation**:
  Replace UAX #29 word counting with a dedicated BPE tokenizer (e.g., `tiktoken-go` with `cl100k_base` or `o200k_base` encodings) or a calibrated token-ratio heuristic tailored to code/markdown ($\approx 3.7$ characters per token) with an explicit variance margin noted in manifest metadata.

---

### 8. Dependency Inconsistency: Dual Fiber v2 and Fiber v3 in `go.mod` - Severity: Nit
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 8-9 in `go.mod`, Line 80 in `docs/designs/engram.md`
* **Discrepancy Description**:
  `go.mod` lists both `github.com/gofiber/fiber/v2 v2.52.16` and `github.com/gofiber/fiber/v3 v3.7.0`. Line 80 specifies "Fiber v2 Router + Huma v2 OpenAPI". Fiber v3 is a major rewrite with incompatible routing and middleware signatures. Importing both versions introduces dependency bloat and confusion.
* **Rationale & Recommendation**:
  Remove `github.com/gofiber/fiber/v3` from `go.mod` and standardize on Fiber v2, which has stable OpenAPI integration via Huma v2 (`humafiber`).

---

### 9. Suboptimal Index Filtering on `idx_engram_tasks_claimable` - Severity: Nit
* **Reviewer Lens:** `egm-inventive-catalyst`
* **Affected Scope:** Lines 238-239
* **Discrepancy Description**:
  The partial index `idx_engram_tasks_claimable ON engram_tasks(project_id, queue, status, priority DESC, created_at ASC) WHERE status IN ('PENDING', 'CLAIMED');` indexes all claimed tasks regardless of expiration. Long-running active tasks will continuously pollute the index, forcing lease-stealing scans to evaluate non-expired rows.
* **Rationale & Recommendation**:
  Split into two specialized partial indexes: one for `status = 'PENDING'` (the primary fast-path queue claim) and a secondary index on `(project_id, queue, lease_expires_at)` `WHERE status = 'CLAIMED'` to isolate expired task discovery without indexing active tasks.

---

### 10. PostgreSQL NOTIFY 8,000-Byte Payload Limit - Severity: Nit
* **Reviewer Lens:** `egm-zoom-out`
* **Affected Scope:** Lines 346, 402
* **Discrepancy Description**:
  PostgreSQL enforces a hard 8,000-byte limit on `NOTIFY` payload strings. If database triggers serialize full diffs or large task payloads into the notification channel, transactions modifying substantial memories will fail with `payload string too long`.
* **Rationale & Recommendation**:
  Explicitly mandate in the architectural invariants that `NOTIFY` payloads only pass lightweight identifiers (`{ "project_id": "...", "key": "...", "event_id": 123 }`), requiring the listener worker to fetch content on demand if needed.
