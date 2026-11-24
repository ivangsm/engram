# Goldfish Review Report: Stage 7 Critic Review

**Target:** `docs/designs/engram.md` (lines 1-384)

## Reviewer Status

| Reviewer | Status |
| --- | --- |
| `egm-design` | completed |
| `egm-structural` | completed |
| `egm-style` | completed |

## Prioritized Findings (Blockers -> Major -> Nits)

### 1. Revision Monotonic Key Collision & Head Desynchronization on Conflict Branching - Severity: Blocker
* **Reviewer Lens:** `egm-design`
* **Affected Scope:** Lines 176-197, 285-307
* **Discrepancy Description**: In `engram_revisions`, the primary key is `(project_id, key, rev)`. Sequence 2 specifies that when Agent B encounters a conflict against rev 2 (`current_hash: "h1"`), Engram appends "rev 3 (hash: 'h2', parent: 'h0', is_conflict: true)" without updating `engram_memories.current_hash` or `head_rev`. If Agent A or another agent subsequently submits a valid update to `h1`, it will attempt to assign revision 3 (since head is currently rev 2), triggering a primary key collision (`project_id, key, 3`). Furthermore, because `engram_memories` only stores a single `current_hash` and `head_rev`, the database schema provides no structural index or relational pointer to track conflicting branch tips, leaving divergent revisions unaddressable.
* **Rationale & Recommendation**: Monotonic linear integers cannot represent concurrent tree branching under a composite primary key. Modify `engram_revisions` to use revision hashes or branch identifiers for uniqueness (e.g. `PRIMARY KEY (project_id, key, hash)` or introduce a `branch_id TEXT NOT NULL DEFAULT 'main'`), and add an explicit `engram_branches` table or `conflicts JSONB / branch_tip TEXT` pointer to `engram_memories`.

### 2. PostgreSQL NOTIFY 8KB Payload Size Limit Violation - Severity: Blocker
* **Reviewer Lens:** `egm-design`
* **Affected Scope:** Lines 88-109, 340-348
* **Discrepancy Description**: Section 4 states that on database mutations, a PostgreSQL trigger fires `NOTIFY engram_events, payload`, and the dedicated worker decodes this payload to broadcast over SSE. PostgreSQL enforces a strict, hard-coded 8,000-byte (8 KB) ceiling on `NOTIFY` payload strings. When storing architectural memories, large code guidelines, or unified diffs that exceed 8KB, the PostgreSQL trigger will raise an immediate error (`payload string too long`), causing the entire enclosing mutating transaction (`store_memory`, `complete_task`) to abort.
* **Rationale & Recommendation**: Redesign the notification contract so that the PostgreSQL trigger emits only a minimal notification envelope (e.g., `{"project_id": "...", "key": "...", "event_type": "...", "rev": 2}`). The `engramd` listener worker must either fan out this lightweight envelope or fetch full contents from the database when needed, guaranteeing payloads never exceed PostgreSQL's 8KB constraint.

### 3. Poison Pill Task Starvation Loop in Expired Lease Stealing - Severity: Blocker
* **Reviewer Lens:** `egm-design`
* **Affected Scope:** Lines 217-240, 309-339
* **Discrepancy Description**: Sequence 3 and the task engine specify dynamic lease stealing via `SKIP LOCKED` when `status = 'CLAIMED' AND lease_expires_at < now()`. However, `engram_tasks` lacks a `max_retries` / `max_claims` column or dead-letter queue mechanism. If a task contains a defect or payload that causes the claiming agent to crash repeatedly (a poison pill), the task will repeatedly expire, be re-claimed by the next agent, crash that agent, and loop infinitely. While `claim_count` is incremented, no guard exists in DDL or query logic to transition the task to `'FAILED'` or `'BLOCKED'` once a threshold is exceeded.
* **Rationale & Recommendation**: Add `max_retries INT NOT NULL DEFAULT 3` to `engram_tasks`. Update the atomic lease claim query and reaper logic so that if `claim_count >= max_retries`, the task automatically transitions to `status = 'FAILED'` with a terminal tombstone in `engram_task_handoffs`, preventing worker crash cascades.

### 4. Contradiction in Elimination of Vector Dimension Rigidity - Severity: Major
* **Reviewer Lens:** `egm-design`
* **Affected Scope:** Lines 112-113, 156-174
* **Discrepancy Description**: Lines 112-113 explicitly claim that the relational schema is designed "To eliminate vector dimension rigidity". However, lines 162-163 hardcode fixed columns `embedding_1536 VECTOR(1536)` and `embedding_768 VECTOR(768)` in `engram_embeddings`, along with static partial HNSW indexes. Adopting any new or standard embedding dimension (e.g. 384 for fast local embeddings, 1024 for Cohere/BGE, 3072 for OpenAI large) requires a destructive schema migration (`ALTER TABLE ADD COLUMN`) and manual index creation.
* **Rationale & Recommendation**: Either remove the claim of eliminating dimension rigidity and explicitly document the schema as fixed dual-dimension (1536 and 768), or implement a model-partitioned or dynamic vector projection strategy where table definitions are decoupled per model/dimension family.

### 5. Omission of Trigger DDL and Missing Handoff Verdict Check Constraint - Severity: Major
* **Reviewer Lens:** `egm-structural`
* **Affected Scope:** Lines 242-257, 340-348
* **Discrepancy Description**: Section 4 relies on a PostgreSQL trigger to fire `NOTIFY engram_events`, but the concrete DDL in Section 2 omits the trigger function (`trigger_engram_events()`) and trigger declaration (`CREATE TRIGGER ...`) entirely. Additionally, while `engram_tasks.status` enforces valid states with a `CHECK` constraint (lines 224-225), `engram_task_handoffs.verdict` (line 248) has no `CHECK` constraint in the DDL, despite Stage 3 (line 428) explicitly asserting that verdicts must belong to `['PASS', 'FAIL', 'BLOCKED', 'NEEDS_REVIEW', 'HANDOFF']`.
* **Rationale & Recommendation**: Include the complete trigger function and trigger definition DDL in Section 2. Add an explicit check constraint to `engram_task_handoffs`: `CHECK (verdict IN ('PASS', 'FAIL', 'BLOCKED', 'NEEDS_REVIEW', 'HANDOFF'))`.

### 6. Cleartext Bearer Token Exposure via SSE Query Parameters - Severity: Major
* **Reviewer Lens:** `egm-design`
* **Affected Scope:** Lines 79, 437
* **Discrepancy Description**: Stage 4 specifies supporting `?token=` query parameters for SSE authentication to accommodate EventSource clients. Passing long-lived pre-shared Bearer tokens in URL query strings violates security best practices, as query strings are routinely recorded in HTTP access logs, proxy logs (e.g., Tailscale exit nodes, reverse proxies), and system error telemetry.
* **Rationale & Recommendation**: Replace static Bearer token query parameters with a short-lived ticket handshake: the client performs an authenticated `POST /api/v1/sse/ticket` with an `Authorization: Bearer <secret>` header to obtain an ephemeral, single-use ticket (TTL < 30 seconds), which is then passed as `GET /sse?ticket=<ticket_id>`.

### 7. Incomplete Conflict Resolution Protocol and Lifecycle - Severity: Major
* **Reviewer Lens:** `egm-structural`
* **Affected Scope:** Lines 53-57, 285-307
* **Discrepancy Description**: Sequence 2 demonstrates how Engram returns `status: "CONFLICT"` and stores divergent revisions with `is_conflict: true`. However, the document does not define the resolution lifecycle: there is no specification of how an agent marks a conflict resolved, what MCP tool is invoked (e.g. `resolve_conflict` vs re-calling `store_memory`), or whether orphaned conflict revisions remain indefinitely in `engram_revisions` and affect progressive disclosure manifests.
* **Rationale & Recommendation**: Specify the complete reconciliation lifecycle: document the MCP tool call used to complete a merge, how `is_conflict` revisions are excluded from `list_manifest` and standard hydration, and how resolved branches are squashed or marked as resolved in `engram_revisions`.

### 8. Missing CUJs for Episodic Logging, Hybrid Retrieval, and Squashing - Severity: Major
* **Reviewer Lens:** `egm-structural`
* **Affected Scope:** Lines 28-34, 47-62, 199-215
* **Discrepancy Description**: The Critical User Journeys section (lines 47-62) includes only 3 scenarios (discovery, mutation conflict, task crash). It completely lacks CUJs for: (1) Episodic Narrative Logging (`engram_episodes`), (2) Resilient Dual-Tier Retrieval / Hybrid Search (Goal G6), and (3) Blame inspection and Revision Squashing (Goal G2, G3). Without these CUJs, system behavior during session handoffs and fulltext fallback is unspecified.
* **Rationale & Recommendation**: Add CUJ 4 (Episodic Event Logging during session transitions), CUJ 5 (Dual-Tier Hybrid Search fallback when embeddings are absent), and CUJ 6 (Blame Traversal & History Squashing).

### 9. Parameter Naming Inconsistency for Optimistic Concurrency - Severity: Nit
* **Reviewer Lens:** `egm-style`
* **Affected Scope:** Lines 55, 296, 301
* **Discrepancy Description**: In CUJ 2 (line 55), the optimistic concurrency parameter is named `expected_parent_hash: h_0`. In Sequence 2 (lines 296, 301), the parameter is named `expected_parent: "h0"`.
* **Rationale & Recommendation**: Standardize on `expected_parent_hash` across all CUJs, sequence diagrams, and MCP tool schemas.

### 10. Unsubstantiated Quantitative Claims and Subjective Jargon - Severity: Nit
* **Reviewer Lens:** `egm-style`
* **Affected Scope:** Lines 11, 19, 44
* **Discrepancy Description**: Line 19 asserts that static reference prose consumes "40–80% of available prompt tokens" without providing baseline model context sizes or token assumptions. Line 11 uses the subjective marketing term "ergonomic companion CLI". Line 44 uses process meta-jargon ("validated during the multi-lens ideation swarm").
* **Rationale & Recommendation**: Replace "40-80%" with concrete token figures (e.g., "typical raw memory dumps consume 4,000–8,000 tokens per invocation"). Replace "ergonomic companion CLI" with "standardized developer CLI". Replace the open question fluff with technical open items or an objective statement of validation.

### 11. Ambiguity in Non-Goals Regarding Background Processes - Severity: Nit
* **Reviewer Lens:** `egm-style`
* **Affected Scope:** Lines 40-41, 88, 344
* **Discrepancy Description**: Non-Goal NG4 states that "Engram avoids background cron or lease-reaping daemons", while Section 1 and Section 4 explicitly architect a persistent background worker (`Listener: Isolated PG LISTEN Worker`). While NG4 targets periodic reaper crons, its heading "External Daemon Scheduling" and phrasing could be misconstrued as banning internal daemon worker loops.
* **Rationale & Recommendation**: Clarify NG4 title and text to explicitly distinguish between "Scheduled Polling/Reaper Daemons" (avoided via dynamic query eviction) and "Persistent Event Listener Workers" (required for SSE fan-out).
