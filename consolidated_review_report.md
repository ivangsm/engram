# Consolidated Review Report: Engram Architecture & Design

**Target:** `docs/designs/engram.md` (Lines 1–460)  
**Date:** 2026-11-23  
**Review Pipeline:** Elephant & Goldfish Model (EGM) Multi-Stage Swarm  
**Overall Verdict:** ⚠️ **APPROVED WITH CONDITIONS (MANDATORY ARCHITECTURAL HARDENING)**

---

## 1. Executive Summary & Review Verdict

The design document `docs/designs/engram.md` presents a compelling, high-performance architecture for an agentic memory and operational state plane in Go backed by PostgreSQL 18. Its progressive disclosure model (skeletal manifests with on-demand hydration), cryptographic blame engine, and atomic queue concurrency (`SKIP LOCKED`) address the acute context-window and synchronization bottlenecks of flat Git markdown files.

However, five sequential review lenses (Zoom Out, Comprehension, Critic, Intermediate Zoom Out, and Implementation Readiness) identified critical failure modes and architectural gaps. Most notably:
1. **Ephemeral LISTEN/NOTIFY and 8KB Payload Limit:** Relying on PostgreSQL `NOTIFY` triggers violates production stability due to the hard 8,000-byte payload limit and lack of reconnect replay. It must be replaced by a durable Transactional Outbox pattern (`engram_events`).
2. **Accidental DAG Branching vs. Strict OCC:** Storing conflicting branches with `is_conflict = true` causes primary key collisions on `(project_id, key, rev)` and orphaned state. Engram must use standard Optimistic Concurrency Control (OCC returning HTTP 409 Conflict with diff and current HEAD).
3. **Task Lease Fencing & Poison-Pill Loops:** Tasks can suffer split-brain mutations from zombie workers and infinite crash loops unless lease fencing (`WHERE lease_id = $lease_id`), `max_retries` thresholds, and atomic CTE stealing are enforced.
4. **Missing MCP Tools & Historical Content:** Operational tools (`create_task`, `fail_task`, `record_handoff`, `search_memories`, `query_episodes`) and full historical `content` in `engram_revisions` were omitted.

This consolidated report synthesizes and deduplicates all findings into 24 actionable items across three priority tiers (6 Blockers, 10 Major, 8 Nits) and provides a Traceability Matrix and Concrete Action Plan to update `docs/designs/engram.md`.

---

## 2. Review Participation & Stage Summary

| Stage | Review Stage Name | Reviewer Subagents | Findings Generated |
| :--- | :--- | :--- | :--- |
| **Stage 5** | Initial Zoom Out Review | `egm-zoom-out`, `egm-inventive-catalyst` | 3 Blockers, 4 Major, 3 Nits |
| **Stage 6** | Comprehension Test | `egm-comprehension` | 2 Blockers, 5 Major, 3 Nits |
| **Stage 7** | Critic Review | `egm-design`, `egm-structural`, `egm-style` | 3 Blockers, 5 Major, 3 Nits |
| **Stage 8** | Intermediate Zoom Out Review | `egm-zoom-out` | 3 Blockers, 2 Major, 0 Nits |
| **Stage 9** | Implementation Readiness Test | `egm-implementation-readiness`, `egm-codebase-details`, `egm-style` | 4 Blockers, 5 Major, 2 Nits |

---

## 3. Prioritized Consolidated Findings

### Tier 1: 🔴 Blockers (Must be resolved before finalizing design & production code)

#### CF-01: Ephemeral LISTEN/NOTIFY and 8KB Payload Limit vs. Durable Transactional Outbox Pattern
- **Stages Raised:** Stage 5 (5.1, 5.10), Stage 6 (6.2.3), Stage 7 (7.2, 7.5), Stage 8 (8.2), Stage 9 (9.1)
- **Problem:** The original design relied on PL/pgSQL triggers firing `NOTIFY engram_events, payload` bridged to in-memory Go channels for SSE streaming (`GET /sse`). This violates two critical production invariants:
  1. PostgreSQL enforces a hard 8,000-byte ceiling on `NOTIFY` payload strings. Any memory revision, architectural guideline, or unified diff exceeding 8KB causes the trigger to raise a fatal error (`payload string too long`), aborting the mutating transaction (`store_memory`, `complete_task`).
  2. PostgreSQL `NOTIFY` is completely ephemeral and connection-scoped. Network jitter, Tailscale route reconnects, or daemon restarts result in permanent event loss because there is no persistent replay buffer or `Last-Event-ID` offset recovery.
- **Impact:** Systemic write failure on medium/large memories and silent event loss across remote nodes.
- **Resolution:**
  1. Implement a durable transactional outbox table in PostgreSQL 18:
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
  2. In mutating Go services (`store_memory`, `claim_task`, etc.), insert the outbox record inside the same `pgx.Tx` transaction and execute a zero-payload wake-up ping: `NOTIFY engram_events, 'ping'`.
  3. The listener receives the ping, queries `SELECT * FROM engram_events WHERE id > $last_seen_id ORDER BY id ASC`, and broadcasts events to in-memory SSE subscriber channels.
  4. The SSE endpoint implements standard `Last-Event-ID: N` header replay, serving all missed events `WHERE id > N` upon client reconnection.

---

#### CF-02: Concurrency Model Conflict: Accidental Git DAG Branching vs. Strict Optimistic Concurrency Control (OCC)
- **Stages Raised:** Stage 5 (5.3), Stage 6 (6.2.2), Stage 7 (7.1, 7.7), Stage 8 (8.1), Stage 9 (9.3)
- **Problem:** Sequence 2 and Section 2 specified that upon concurrent write collisions, Engram appends a revision with `is_conflict = true` to `engram_revisions`. However, `engram_revisions` has `PRIMARY KEY (project_id, key, rev)`. If revision 2 is HEAD, attempting to append a divergent revision assigns rev 3 while leaving `engram_memories.head_rev` at 2. A subsequent write to HEAD will attempt to assign rev 3 again, triggering a fatal primary key collision. Furthermore, `engram_memories` has no pointer for branch tips, leaving divergent revisions orphaned, while introducing multi-parent DAG merge complexity that violates Non-Goal NG2 ("Direct Git Automation is out of scope").
- **Impact:** Database constraint violations, unaddressable orphaned revisions, and massive accidental complexity.
- **Resolution:**
  1. Prune speculative DAG branching and standardize on strict Optimistic Concurrency Control (OCC):
  2. When an agent submits `store_memory` with an `expected_parent_hash` that does not match `engram_memories.current_hash`, reject the write immediately with HTTP `409 Conflict` (`ERR_STALE_REVISION`).
  3. The rejection payload returns `{ head_rev, head_hash, current_content, diff_unified }`.
  4. The calling agent reconciles the divergence in its own context window and resubmits targeting the updated HEAD.
  5. `engram_revisions` remains strictly linear and monotonic, incrementing `rev` only on accepted mutations.

---

#### CF-03: Task State Plane Vulnerabilities: Split-Brain Execution, Poison-Pill Starvation Loops & Multi-Step Leases
- **Stages Raised:** Stage 5 (5.2), Stage 6 (6.2.4), Stage 7 (7.3), Stage 8 (8.3)
- **Problem:** The task engine contained three critical concurrency gaps:
  1. **Lack of Lease Fencing:** Task mutations (`complete_task`, `renew_task_lease`, `fail_task`) did not validate `lease_id`. A worker experiencing a GC pause or network stall exceeding its lease TTL could wake up after its lease was stolen and overwrite the active worker's state, corrupting tasks.
  2. **Poison-Pill Crash Loops:** If a task contained a payload that deterministically crashed workers, consecutive agents would repeatedly claim, crash, expire, and re-claim the task indefinitely because no maximum claim threshold existed.
  3. **Multi-Step Stealing:** Expired lease stealing and diagnostic tombstone creation were split into two separate application queries, risking missing audit records if the daemon restarted mid-step.
- **Impact:** Split-brain task corruption, agent cascade crashes, and compromised audit trails.
- **Resolution:**
  1. Enforce strict lease fencing: Every task mutation MUST include `WHERE project_id = $1 AND id = $2 AND lease_id = $3`. If 0 rows are updated, return HTTP `409 Conflict` (`ERR_LEASE_LOST`).
  2. Add `max_retries INT NOT NULL DEFAULT 3` to `engram_tasks`.
  3. Combine lease stealing, poison-pill transition (`status = 'FAILED'`), and tombstone insertion into a single atomic Common Table Expression (CTE):
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

---

#### CF-04: Missing Core Operational Workflows in MCP Tools, Queries & Services
- **Stages Raised:** Stage 6 (6.2.1), Stage 7 (7.8), Stage 9 (9.2)
- **Problem:** The MCP tool registry (`internal/mcp/tools.go`) omitted essential tools required by the system specification:
  1. `create_task` / `enqueue_task`: No tool existed to create tasks, preventing agents or human orchestrators from enqueuing work.
  2. `fail_task`: The schema permitted status `'FAILED'` and `'BLOCKED'`, but agents only had `complete_task`.
  3. `record_handoff`: Agent-to-agent collaborative handoffs had domain logic in `handoff.go`, but no MCP tool to submit them.
  4. `search_memories`: Goal G6 specified dual-tier hybrid retrieval, but no MCP search tool was registered.
  5. `query_episodes`: Fulltext episodic persistence was write-only (`log_episode`) with no retrieval tool.
- **Impact:** System unusable for full-lifecycle task tracking, structured handoffs, and semantic search.
- **Resolution:** Add `create_task`, `fail_task`, `record_handoff`, `search_memories`, and `query_episodes` to `internal/mcp/tools.go`, SQL queries, and domain service interfaces.

---

#### CF-05: Missing `content` Persistence in `engram_revisions` Breaks Historical Blame & Content Hydration
- **Stages Raised:** Stage 6 (6.3.1), Stage 8 (8.5)
- **Problem:** The `engram_revisions` table schema stored only `diff TEXT NOT NULL DEFAULT ''`, omitting full content. Full content existed solely in `engram_memories.content` for HEAD. Consequently:
  1. Historical revisions could not be inspected or restored without recursively applying unified diffs backwards from HEAD across all revisions, which fails if any diff has whitespace discrepancies or patch drift.
  2. Revision 1 has no prior revision to diff against, leaving its initial content representation undefined.
- **Impact:** Broken $O(1)$ historical blame inspection and fragile history reconstruction.
- **Resolution:** Add `content TEXT NOT NULL` directly to `engram_revisions` alongside `diff TEXT`. Text is natively compressed via PostgreSQL TOAST, providing instant $O(1)$ retrieval and guaranteed diff verification without storage bloat.

---

#### CF-06: Unspecified Vector Embedding Ingestion Pipeline & Architecture
- **Stages Raised:** Stage 5 (5.4), Stage 6 (6.3.2), Stage 7 (7.4), Stage 9 (9.4)
- **Problem:** The schema defined fixed columns `embedding_1536 VECTOR(1536)` and `embedding_768 VECTOR(768)` while claiming to "eliminate vector dimension rigidity". Furthermore, the implementation plan never defined who generates embeddings (daemon vs. client), what client is used, or the failure mode when embedding APIs fail or are unconfigured.
- **Impact:** Implementation block and model lock-in.
- **Resolution:**
  1. Define `internal/platform/embedding/client.go` with an `Embedder` interface supporting external APIs (e.g., OpenAI `text-embedding-3-small`), Ollama/local HTTP, and a No-Op fallback.
  2. Specify synchronous embedding generation on `store_memory` with graceful degradation: if no embedding provider is configured or if the API times out, vector insertion is skipped and retrieval queries transparently fall back to fulltext `tsvector`.
  3. Document supported dimension profiles (1536 and 768) in the design invariants.

---

### Tier 2: 🟡 Major Findings (Should be resolved to avoid technical debt)

#### CF-07: YAGNI: Elimination of Revision Squashing to Preserve Append-Only Provenance
- **Stages Raised:** Stage 5 (5.5), Stage 8 (8.4)
- **Problem:** Lines 188–189 introduced `is_squashed` and `squashed_into_rev`, and Stage 4 defined `squash_revisions`. Squashing destroys the linear SHA-256 parent-hash chain, introduces complex boundary metadata, and contradicts Goal G2 (Cryptographic Blame & Provenance). Text entries are small (<50KB) and TOAST-compressed; squashing saves negligible storage while introducing massive algorithmic fragility.
- **Resolution:** Prune squashing entirely (YAGNI). Drop `is_squashed` and `squashed_into_rev` from `engram_revisions`, remove `squash_revisions` tool, and maintain an immutable linear ledger.

#### CF-08: Inaccurate Context Budget Estimation via Word Segmentation (`uax29`)
- **Stages Raised:** Stage 5 (5.7), Stage 6 (6.1.2), Stage 9 (9.7)
- **Problem:** UAX #29 standardizes word segmentation, undercounting LLM BPE subword tokens by 30% to 60% in source code and structured markdown, breaking Goal G1's manifest budget (<1,500 tokens).
- **Resolution:** Standardize on a dedicated BPE tokenizer (`tiktoken-go` with `cl100k_base` encoding) or an explicit calibrated heuristic ($\approx 3.7$ characters per token).

#### CF-09: Unbounded Table Growth & Lack of Partitioning / Retention Policies
- **Stages Raised:** Stage 5 (5.6)
- **Problem:** High-frequency CI runs and autonomous agent loops rapidly inflate `engram_episodes` and `engram_revisions`, degrading GIN indexes over time.
- **Resolution:** Add declarative range partitioning on `engram_episodes` by `created_at` (monthly) and define a 90-day retention policy with CLI administrative command `engram prune`.

#### CF-10: Incomplete DDL Constraints & Missing Database Trigger Function
- **Stages Raised:** Stage 6 (6.2.3), Stage 7 (7.5)
- **Problem:** `engram_task_handoffs.verdict` lacked a `CHECK` constraint in DDL, and trigger functions were referenced in prose but omitted from SQL.
- **Resolution:** Add `CHECK (verdict IN ('PASS', 'FAIL', 'BLOCKED', 'NEEDS_REVIEW', 'HANDOFF', 'LEASE_EXPIRED'))` to `engram_task_handoffs`. Trigger DDL is superseded by the Transactional Outbox pattern in CF-01.

#### CF-11: Cleartext Bearer Token Exposure via SSE Query Parameters
- **Stages Raised:** Stage 7 (7.6)
- **Problem:** Passing static Bearer tokens in URL query strings (`GET /sse?token=...`) exposes credentials in access logs and proxy telemetry.
- **Resolution:** Implement a short-lived ephemeral ticket handshake: `POST /api/v1/sse/ticket` with Bearer auth returns a single-use ticket (TTL 30s) used as `GET /sse?ticket=<id>`.

#### CF-12: Missing CUJs for Episodic Logging, Hybrid Retrieval, and Provenance Blame
- **Stages Raised:** Stage 7 (7.8)
- **Problem:** The design document included only 3 CUJs, omitting episodic session handoffs, hybrid retrieval fallback, and blame inspection.
- **Resolution:** Add CUJ 4 (Episodic Event Logging), CUJ 5 (Dual-Tier Hybrid Search), and CUJ 6 (Blame Traversal & Provenance Verification).

#### CF-13: Omission of Foundational Go Project Files & Configuration Engine
- **Stages Raised:** Stage 9 (9.5)
- **Problem:** The implementation plan jumped to `docker-compose.yml` and omitted `go.mod`, `internal/platform/config/config.go`, and `.env.example`.
- **Resolution:** Explicitly specify `go.mod`, `config.go`, and `.env.example` in Stage 1 of the implementation plan.

#### CF-14: Unspecified Cryptographic Blame Serialization and Canonicalization Formula
- **Stages Raised:** Stage 6 (6.3.3), Stage 9 (9.6)
- **Problem:** The canonical preimage format for computing SHA-256 revision hashes was not formally specified.
- **Resolution:** Define the exact preimage format:
  `canonicalPreimage := fmt.Sprintf("parent:%s\nkey:%s\nauthor:%s\nmodel:%s\nmachine:%s\ncontent:\n%s", parentHash, key, author, agentModel, machineId, normalizeLF(content))`. Root revision (rev 1) uses `parent_hash = ""`.

#### CF-15: Monolithic CLI Command Architecture & Communication Protocol Underspecified
- **Stages Raised:** Stage 9 (9.8)
- **Problem:** `cmd/engram/main.go` concentrated all 8 commands into a single file without modular command architecture or shared client library.
- **Resolution:** Modularize CLI using `github.com/spf13/cobra` into `cmd/engram/commands/` and introduce `internal/client/client.go`.

#### CF-16: Underspecified Integration Test Execution Strategy & Database Harness
- **Stages Raised:** Stage 9 (9.9)
- **Problem:** Integration tests requiring PostgreSQL 18 extensions lacked an execution harness and isolation strategy.
- **Resolution:** Add `internal/platform/testutil/testdb.go` reading `ENGRAM_TEST_DATABASE_URL` with transactional rollback isolation.

---

### Tier 3: 🟢 Minor Polish & Nits

#### CF-17: Dependency Inconsistency: Dual Fiber v2 and Fiber v3 in `go.mod`
- **Stages Raised:** Stage 5 (5.8)
- **Resolution:** Remove `github.com/gofiber/fiber/v3` and standardize strictly on Fiber v2 with Huma v2.

#### CF-18: Suboptimal Index Filtering on `idx_engram_tasks_claimable`
- **Stages Raised:** Stage 5 (5.9)
- **Resolution:** Split into specialized partial indexes: one for `status = 'PENDING'` and one for `status = 'CLAIMED'`.

#### CF-19: Parameter Naming Inconsistency (`expected_parent_hash` vs `expected_parent`)
- **Stages Raised:** Stage 7 (7.9)
- **Resolution:** Standardize on `expected_parent_hash` across all CUJs, sequence diagrams, and MCP tool schemas.

#### CF-20: Unsubstantiated Quantitative Claims and Subjective Jargon
- **Stages Raised:** Stage 7 (7.10)
- **Resolution:** Replace "40-80%" with concrete token figures, "ergonomic companion CLI" with "standardized developer CLI", and remove ideation meta-jargon.

#### CF-21: Ambiguity in Non-Goals Regarding Background Processes
- **Stages Raised:** Stage 7 (7.11)
- **Resolution:** Clarify NG4 to distinguish between periodic cron reapers (avoided via dynamic query eviction) and event listener workers (required for SSE).

#### CF-22: Undefined Internal Paths and Acronyms
- **Stages Raised:** Stage 6 (6.1.1, 6.1.3)
- **Resolution:** Clarify `.serena/memories/*.md` and `checamos-docs/` as examples of legacy documentation conventions.

#### CF-23: Inconsistent Handoff Field Terminology (`reason` vs `summary`, `evidence` in metadata)
- **Stages Raised:** Stage 9 (9.10)
- **Resolution:** Clarify that the <150 word limit applies to `reason` and evidence is stored in `metadata->>'evidence'`.

#### CF-24: Missing Canonical Go Module Import Paths
- **Stages Raised:** Stage 9 (9.11)
- **Resolution:** Specify exact import paths for `difflib`, `Fiber`, `Huma`, and `pgx` in implementation plan.

---

## 4. Traceability Matrix

| Stage | Raw Finding ID | Raw Finding Title | Consolidated ID | Consolidated Severity |
| :--- | :--- | :--- | :--- | :--- |
| **Stage 5** | 5.1 | Ephemeral SSE Event Streaming & Desync | **CF-01** | 🔴 Blocker |
| **Stage 5** | 5.2 | Atomic Lease Stealing Concurrency Race | **CF-03** | 🔴 Blocker |
| **Stage 5** | 5.3 | Orphaned Conflict Branches & Missing Lifecycle | **CF-02** | 🔴 Blocker |
| **Stage 5** | 5.4 | Rigid Vector Dimension Columns Contradicting Goals | **CF-06** | 🔴 Blocker |
| **Stage 5** | 5.5 | Cryptographic Hash Chain Breakage on Squashing | **CF-07** | 🟡 Major |
| **Stage 5** | 5.6 | Unbounded Growth & Search Latency Degradation | **CF-09** | 🟡 Major |
| **Stage 5** | 5.7 | Inaccurate Context Budget Estimation via `uax29` | **CF-08** | 🟡 Major |
| **Stage 5** | 5.8 | Dual Fiber v2 and Fiber v3 in `go.mod` | **CF-17** | 🟢 Nit |
| **Stage 5** | 5.9 | Suboptimal Index Filtering on Tasks | **CF-18** | 🟢 Nit |
| **Stage 5** | 5.10 | PostgreSQL NOTIFY 8,000-Byte Payload Limit | **CF-01** | 🔴 Blocker |
| **Stage 6** | 6.1.1 | Undefined internal repo paths and identifiers | **CF-22** | 🟢 Nit |
| **Stage 6** | 6.1.2 | Undefined token estimation algorithm (`uax29`) | **CF-08** | 🟡 Major |
| **Stage 6** | 6.1.3 | Unexplained third-party Go dependencies | **CF-24** | 🟢 Nit |
| **Stage 6** | 6.2.1 | Missing MCP tools for tasks, handoffs, search | **CF-04** | 🔴 Blocker |
| **Stage 6** | 6.2.2 | Undefined conflict resolution protocol | **CF-02** | 🔴 Blocker |
| **Stage 6** | 6.2.3 | Undefined database trigger mechanism | **CF-01**, **CF-10**| 🔴 Blocker / 🟡 Major |
| **Stage 6** | 6.2.4 | Infinite crash loops & missing DLQ | **CF-03** | 🔴 Blocker |
| **Stage 6** | 6.3.1 | Missing `content` column in `engram_revisions` | **CF-05** | 🔴 Blocker |
| **Stage 6** | 6.3.2 | Rigid vector column dimensions | **CF-06** | 🔴 Blocker |
| **Stage 6** | 6.3.3 | Undefined canonical serialization & hash formula | **CF-14** | 🟡 Major |
| **Stage 7** | 7.1 | Revision Key Collision & Head Desync on Conflict | **CF-02** | 🔴 Blocker |
| **Stage 7** | 7.2 | PostgreSQL NOTIFY 8KB Payload Limit Violation | **CF-01** | 🔴 Blocker |
| **Stage 7** | 7.3 | Poison Pill Task Starvation Loop in Leases | **CF-03** | 🔴 Blocker |
| **Stage 7** | 7.4 | Contradiction in Eliminating Vector Rigidity | **CF-06** | 🔴 Blocker |
| **Stage 7** | 7.5 | Omission of Trigger DDL & Missing Handoff Check | **CF-01**, **CF-10**| 🔴 Blocker / 🟡 Major |
| **Stage 7** | 7.6 | Cleartext Bearer Token Exposure via SSE Query | **CF-11** | 🟡 Major |
| **Stage 7** | 7.7 | Incomplete Conflict Resolution Protocol | **CF-02** | 🔴 Blocker |
| **Stage 7** | 7.8 | Missing CUJs for Episodes, Retrieval, Squashing | **CF-12** | 🟡 Major |
| **Stage 7** | 7.9 | Parameter Naming Inconsistency (`expected_parent`)| **CF-19** | 🟢 Nit |
| **Stage 7** | 7.10 | Unsubstantiated Quantitative Claims & Jargon | **CF-20** | 🟢 Nit |
| **Stage 7** | 7.11 | Ambiguity in Non-Goals Regarding Daemons | **CF-21** | 🟢 Nit |
| **Stage 8** | 8.1 | Git-Like DAG Branching vs. Standard OCC | **CF-02** | 🔴 Blocker |
| **Stage 8** | 8.2 | LISTEN/NOTIFY & 8KB Limit vs. Transactional Outbox | **CF-01** | 🔴 Blocker |
| **Stage 8** | 8.3 | Split-Brain Mutations & Poison-Pill Loops | **CF-03** | 🔴 Blocker |
| **Stage 8** | 8.4 | Elimination of Squashing to Preserve Blame (YAGNI)| **CF-07** | 🟡 Major |
| **Stage 8** | 8.5 | Omission of Full Content in Revisions | **CF-05** | 🔴 Blocker |
| **Stage 9** | 9.1 | Ephemeral LISTEN/NOTIFY vs. Durable Outbox Plan | **CF-01** | 🔴 Blocker |
| **Stage 9** | 9.2 | Missing Core Operational Workflows in Tools/Plan | **CF-04** | 🔴 Blocker |
| **Stage 9** | 9.3 | Ambiguous Conflict Handling & PK Collision Plan | **CF-02** | 🔴 Blocker |
| **Stage 9** | 9.4 | Unspecified Vector Embedding Ingestion Pipeline | **CF-06** | 🔴 Blocker |
| **Stage 9** | 9.5 | Omission of Foundational Go Project Files & Config| **CF-13** | 🟡 Major |
| **Stage 9** | 9.6 | Unspecified Cryptographic Blame Serialization | **CF-14** | 🟡 Major |
| **Stage 9** | 9.7 | Ambiguous Token Estimation Algorithm Contract | **CF-08** | 🟡 Major |
| **Stage 9** | 9.8 | CLI Command Architecture & Protocol Underspecified| **CF-15** | 🟡 Major |
| **Stage 9** | 9.9 | Underspecified Integration Test Strategy/Harness | **CF-16** | 🟡 Major |
| **Stage 9** | 9.10 | Inconsistent Handoff Field Terminology | **CF-23** | 🟢 Nit |
| **Stage 9** | 9.11 | Missing Canonical Go Module Import Paths | **CF-24** | 🟢 Nit |

---

## 5. Concrete Action Plan for Updating `docs/designs/engram.md`

1. **Section 1: Architecture Invariants & Overview**
   - Incorporate Durable Outbox pattern (`engram_events`) and clarify persistent event listener worker vs. non-goal cron reapers.
   - Clean up quantitative claims and subjective marketing jargon.
2. **Section 2: PostgreSQL 18 Relational Schema**
   - Add `engram_events` table definition and index.
   - Add `content TEXT NOT NULL` to `engram_revisions`.
   - Remove `is_squashed` and `squashed_into_rev` from `engram_revisions`.
   - Add `max_retries INT NOT NULL DEFAULT 3` to `engram_tasks`.
   - Add `CHECK (verdict IN ('PASS', 'FAIL', 'BLOCKED', 'NEEDS_REVIEW', 'HANDOFF', 'LEASE_EXPIRED'))` to `engram_task_handoffs`.
   - Split `idx_engram_tasks_claimable` into separate indexes for `PENDING` and `CLAIMED`.
3. **Section 3: Interaction Workflows & Sequences**
   - Update CUJ 2 and Sequence 2 to strict OCC returning HTTP `409 Conflict` (`ERR_STALE_REVISION`) with diff and HEAD.
   - Update CUJ 3 and Sequence 3 to atomic CTE lease acquisition with lease fencing (`WHERE lease_id = $lease_id`) and `max_retries` guard.
   - Add CUJ 4 (Episodic Event Logging), CUJ 5 (Dual-Tier Hybrid Search), and CUJ 6 (Blame Traversal & Provenance Verification).
4. **Section 4: Cross-Cutting Architectural Patterns**
   - Detail Durable Outbox streaming over SSE with `Last-Event-ID` replay.
   - Specify ephemeral ticket handshake (`POST /api/v1/sse/ticket`) for secure SSE authentication.
   - Specify canonical SHA-256 preimage format in Blame Engine.
5. **Section 5: Detailed Implementation Plan**
   - Update Stage 1 with `go.mod`, `internal/platform/config/config.go`, and `.env.example`.
   - Update Stage 2 to include `events.sql` queries and prune squashing queries.
   - Update Stage 3 to include `internal/platform/embedding/client.go` and OCC logic.
   - Update Stage 4 MCP tools list to include `create_task`, `fail_task`, `record_handoff`, `search_memories`, `query_episodes` and remove `squash_revisions`.
   - Update Stage 5 with modular Cobra CLI structure and `internal/platform/testutil/testdb.go` test harness.
