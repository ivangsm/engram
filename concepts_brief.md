# Engram: Agentic Memory, Episodic Persistence & Multi-Machine Operational State Plane

* **Author:** Iván Salazar
* **Date:** 2026-11-23 (Synthesized: 2026-11-24)
* **Mode:** BRAINSTORM
* **Status:** Complete (Multi-Lens Swarm Synthesized)

## Reviewer Swarm Status

| Reviewer Shortname | Role Lens | Status |
| --- | --- | --- |
| `egm-design` | Skeptical Architect & API/DX Brainstormer | completed |
| `egm-codebase-details` | PostgreSQL 18 & Go Systems Brainstormer | completed |
| `egm-zoom-out` | Ecosystem & Multi-Machine Topology Brainstormer | completed |
| `egm-comprehension` | Agent Cognitive Load & Protocol Ergonomics Brainstormer | completed |
| `egm-implementation-readiness` | Production Invariants & Edge Cases Brainstormer | completed |
| `egm-structural` | Relational Integrity & Concurrency Brainstormer | completed |
| `egm-inventive-catalyst` | TRIZ Contradictions & Novel Synthesis Brainstormer | completed |

---

## 1. Executive Summary & Problem Framing

Engram is a self-contained, high-performance agentic memory service, episodic/semantic persistence plane, and concurrent task coordination engine designed specifically for software engineering workflows and AI coding agents (Antigravity, Claude Code, Cursor, Codex) alongside human developers.

Operating natively over the Model Context Protocol (MCP) via Streamable HTTP/SSE and a companion CLI, Engram replaces uncoordinated, merge-conflict-prone flat Markdown files (`TODO.md`, `.serena/memories/*.md`, `checamos-docs/`) with a transactional, progressive-disclosure data store.

### Core Problems Solved
1. **Context Window Destruction (Greedy Memory Dumps):** Reading flat `.serena/memories/*.md` files forces agents to load thousands of tokens of reference text upfront. Engram introduces **Skeletal Manifests** (`key`, `digest`, `token_count`, `scope`) with zero plain text dumping until explicitly fetched.
2. **Silent State Clobbering:** Parallel agents writing to shared Markdown files overwrite each other's edits silently. Engram introduces **Optimistic Content-Hash Concurrency** with non-destructive conflict branching (`expected_parent_hash`), preserving divergent revisions for explicit agent-level reconciliation.
3. **Linear Revision Bloat:** Append-only cryptographic blame creates an unbroken audit trail, but high-frequency agent iterations cause massive database bloat. Engram resolves this with **Threshold-Triggered Squashing** (`squash_revisions`).
4. **Stranded Workloads & Split-Brain Execution:** Agent crashes or network drops leave locks stranded indefinitely. Engram introduces **Atomic Task Leases via `SKIP LOCKED`** with automatic reaper handoffs and diagnostic crash tombstones.

---

## 2. Synthesized Multi-Lens Critique & Prioritized Findings

### Blocker Severity (Blocker)

#### 1. Vector Dimension Rigidity Prevents Indexing on Heterogeneous Models
* **Reviewer Lens:** `egm-design`, `egm-codebase-details`, `egm-structural`
* **Affected Scope:** Vector search schema & query engine
* **Discrepancy:** In PostgreSQL 18 with `pgvector`, an unconstrained `vector` column cannot have an HNSW or IVFFlat index created on it; `pgvector` requires explicit dimension declarations (`vector(1536)`). Without an index, similarity queries degrade to sequential scans ($O(N)$), failing performance requirements as memory scale grows.
* **Recommendation:** Decouple base memory text from vector storage. Retain `engram_memories` with deterministic `tsvector` + `pg_trgm` fulltext indexes as the resilient backbone. Store embeddings in a dedicated auxiliary table (`engram_embeddings`) keyed by `(project_id, memory_key, model_id)` with typed dimension columns (e.g. `vector(1536)` for OpenAI `text-embedding-3-small`, `vector(768)` for Ollama `nomic-embed-text`) and dedicated HNSW indexes. When no embedding exists or models change, the query engine automatically executes fulltext rank (`ts_rank`) without schema errors.

#### 2. Long-Lived SSE Connections Risk Database Pool Exhaustion
* **Reviewer Lens:** `egm-codebase-details`
* **Affected Scope:** MCP SSE transport & `pgxpool.Pool` lifecycle
* **Discrepancy:** If Server-Sent Events (SSE) connections hold a connection open from `pgxpool.Pool` to wait for notifications, a small number of concurrent agent or CLI subscribers will saturate the pool, starving transactional read/write operations.
* **Recommendation:** Isolate event subscriptions. Run a single dedicated background Go worker using a dedicated `pgx.Conn` executing PostgreSQL `LISTEN engram_events`. The Go runtime then broadcasts incoming notifications in-memory to active SSE client channels via fan-out (`sync.RWMutex` + channel registry). The standard `pgxpool.Pool` is used strictly for short-lived transactional queries.

#### 3. Task Lease Expiration Without Active Renewal Causes Split-Brain Execution
* **Reviewer Lens:** `egm-implementation-readiness`, `egm-design`
* **Affected Scope:** Task lease protocol & runtime
* **Discrepancy:** If a task lease duration is static (e.g. 180 seconds) and an agent undertakes a long operation (e.g., executing a test suite, building containers), the lease will expire while the agent is still healthy. The reaper reassigns the task to another agent, causing concurrent, conflicting execution on the same task.
* **Recommendation:** Provide an explicit `renew_task_lease(project_id, task_id, lease_id, extend_seconds)` MCP tool and CLI command. Agents invoke this heartbeat during long executions. Reassignment only occurs when heartbeats lapse.

---

### Major Severity (Major)

#### 4. Conflation of Reference Knowledge vs Episodic Event Logs
* **Reviewer Lens:** `egm-comprehension`, `egm-structural`
* **Affected Scope:** Data modeling & mental model
* **Discrepancy:** Conflating persistent reference memory (canonical architecture decisions, contracts) with transient operational history (agent session logs, step-by-step tool invocations) leads to severe revision pollution in reference documents.
* **Recommendation:** Separate the domain into two distinct entities and table structures:
  - `engram_memories`: Keyed, versioned, squashed, searchable reference documents.
  - `engram_episodes`: Append-only, chronological narrative log tagged by `session_id`, `actor`, and `event_type`.

#### 5. Absence of Batch Fetching Increases Network Latency Over Tailscale
* **Reviewer Lens:** `egm-design`, `egm-zoom-out`
* **Affected Scope:** MCP Toolset ergonomics
* **Discrepancy:** In a distributed setup where agents run on a MacBook and Engram runs on a remote VPS (Contabo) over Tailscale (30–100ms round-trip), requiring sequential single-item `get_memory` calls after manifest discovery introduces unacceptable latency.
* **Recommendation:** Expose `get_memories_batch(project_id, keys: []string)` to allow bulk retrieval of selected memory bodies in a single round-trip.

#### 6. Destructive Squashing Breaks Historical Audit Permalinks
* **Reviewer Lens:** `egm-implementation-readiness`, `egm-structural`
* **Affected Scope:** Revision squashing mechanics
* **Discrepancy:** Hard-deleting micro-revisions during `squash_revisions` breaks permalinks from previous task handoffs or commits that cite specific revision numbers.
* **Recommendation:** Non-destructive squashing: mark compacted revisions with `is_squashed = true` and `squashed_into_rev = $rev`, nulling or compressing their diff bodies while preserving header metadata (`rev`, `hash`, `author`, `created_at`).

#### 7. Latency and Token Estimation Inaccuracy
* **Reviewer Lens:** `egm-implementation-readiness`
* **Affected Scope:** Progressive disclosure manifest
* **Discrepancy:** If manifest `token_count` is inaccurate or missing, agents cannot budget their context window before fetching full content.
* **Recommendation:** Calculate token counts deterministically using `uax29` word/token counting upon write, persisting it directly in `engram_memories.token_count`.

---

### Nit Severity (Nit)

#### 8. Canonical Serialization and Whitespace Normalization
* **Reviewer Lens:** `egm-codebase-details`
* **Affected Scope:** Content-hash generation
* **Discrepancy:** Non-normalized line endings (`\r\n` vs `\n`) or trailing whitespace will produce false hash mismatches and spurious merge conflicts across machines.
* **Recommendation:** Enforce strict LF normalization and whitespace stripping before computing SHA-256 hashes.

#### 9. Dynamic Task Status Projection
* **Reviewer Lens:** `egm-implementation-readiness`
* **Affected Scope:** Task query engine
* **Discrepancy:** If an agent queries `list_tasks`, tasks whose lease has expired but haven't yet been claimed by another worker should not display as active `CLAIMED`.
* **Recommendation:** Use dynamic projection in SQL: `CASE WHEN status = 'CLAIMED' AND lease_expires_at < now() THEN 'EXPIRED' ELSE status END AS status`.

---

## 3. Inventive Contradictions & TRIZ Pattern Resolutions

| Technical / Physical Contradiction | Engineering Tension | TRIZ Resolution |
| --- | --- | --- |
| 1. Model Flexibility vs Index Invariants | Index must be rigid for speed, but dynamic for multi-model agility | Principle 1 & 24 (Segmentation & Intermediary) |
| 2. Cryptographic Blame vs Linear Revision Bloat | History must be complete for audit, but compact for context limits | Principle 15 & 10 (Dynamism & Prior Action) |
| 3. Autonomous Lease Stealing vs Silent Failure Masking | Crashed tasks must be reclaimed, but crash causes must not be hidden | Principle 22 (Turn Harm into Good) |

### Contradiction 1: Heterogeneous Vector Geometries vs. Database Index Rigidity
* **Tension:** `pgvector` HNSW indexes require a fixed column dimension (e.g. 1536), but agents utilize heterogeneous embedding models (OpenAI 1536, Nomic 768, Local 384).
* **Resolution (Segmentation & Intermediary):** Decouple text storage from vector indexes.
  - Baseline text search is powered universally by PostgreSQL native `tsvector` with GIN indexing and `pg_trgm` trigram indexing.
  - Vector projections are segmented into `engram_embeddings` rows with typed HNSW indexes.
  - If a model is changed, memories remain instantly searchable via fulltext rank with zero downtime.

### Contradiction 2: Cryptographic Blame Immutability vs. Linear Token/Storage Bloat
* **Tension:** Every agent edit must be cryptographically auditable, yet micro-edits generate hundreds of intermediate rows that degrade query speed and agent comprehension.
* **Resolution (Dynamism & Hierarchical Compaction):**
  - Append-only recording during active work.
  - When revisions exceed threshold (>25), system returns proactive `maintenance_notice`.
  - `squash_revisions` tool compacts micro-revisions into a semantic milestone revision, preserving the initial parent hash, cumulative diff, and list of contributing authors.

### Contradiction 3: Autonomous Task Stealing vs. Diagnostic Transparency
* **Tension:** When an agent crashes, other agents must reclaim the task without manual human intervention; however, blind lease resets mask fatal exceptions and prevent root cause analysis.
* **Resolution (Turn Harm Into Good / Crash Autopsy Tombstone):**
  - The reaper does not just reset a lease pointer. Upon reclaiming an expired lease, Engram automatically inserts an immutable record into `engram_task_handoffs` (`from_agent: 'system/lease-reaper'`, `verdict: 'BLOCKED'`, detailing previous holder, timeout timestamp, and elapsed duration).
  - The reclaiming agent receives this handoff as part of `claim_task`, enabling it to immediately diagnose the crash context before executing.

---

## 4. System Architecture & Concrete PostgreSQL 18 Specifications

### Relational Schema (DDL)

```sql
-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
CREATE EXTENSION IF NOT EXISTS "vector";

-- 1. Projects
CREATE TABLE engram_projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. Canonical Memories (Progressive Disclosure Core)
CREATE TABLE engram_memories (
    project_id TEXT NOT NULL REFERENCES engram_projects(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    current_hash TEXT NOT NULL,
    head_rev INT NOT NULL DEFAULT 1,
    title TEXT NOT NULL,
    digest TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'project',
    content TEXT NOT NULL,
    token_count INT NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    tsv TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(key, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(content, '')), 'C')
    ) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, key)
);

CREATE INDEX idx_engram_memories_tsv ON engram_memories USING GIN(tsv);
CREATE INDEX idx_engram_memories_trgm_key ON engram_memories USING GIN(key gin_trgm_ops);
CREATE INDEX idx_engram_memories_scope ON engram_memories(project_id, scope);

-- 3. Auxiliary Model-Specific Embeddings
CREATE TABLE engram_embeddings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id TEXT NOT NULL,
    memory_key TEXT NOT NULL,
    model TEXT NOT NULL,
    dimensions INT NOT NULL,
    embedding_1536 VECTOR(1536),
    embedding_768 VECTOR(768),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fk_memory FOREIGN KEY (project_id, memory_key)
        REFERENCES engram_memories(project_id, key) ON DELETE CASCADE,
    CONSTRAINT uq_memory_model UNIQUE (project_id, memory_key, model)
);

CREATE INDEX idx_engram_embeddings_1536 ON engram_embeddings 
    USING hnsw (embedding_1536 vector_cosine_ops) WHERE embedding_1536 IS NOT NULL;
CREATE INDEX idx_engram_embeddings_768 ON engram_embeddings 
    USING hnsw (embedding_768 vector_cosine_ops) WHERE embedding_768 IS NOT NULL;

-- 4. Cryptographic Blame & Revisions
CREATE TABLE engram_revisions (
    project_id TEXT NOT NULL,
    key TEXT NOT NULL,
    rev INT NOT NULL,
    hash TEXT NOT NULL,
    parent_hash TEXT NOT NULL,
    author TEXT NOT NULL,
    agent_model TEXT NOT NULL,
    machine_id TEXT NOT NULL,
    diff TEXT NOT NULL DEFAULT '',
    rationale TEXT NOT NULL DEFAULT '',
    is_conflict BOOLEAN NOT NULL DEFAULT false,
    is_squashed BOOLEAN NOT NULL DEFAULT false,
    squashed_into_rev INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, key, rev),
    CONSTRAINT fk_revision_memory FOREIGN KEY (project_id, key)
        REFERENCES engram_memories(project_id, key) ON DELETE CASCADE
);

CREATE INDEX idx_engram_revisions_hash ON engram_revisions(project_id, key, hash);

-- 5. Episodic Event Log (Timeline & Audit Trail)
CREATE TABLE engram_episodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id TEXT NOT NULL REFERENCES engram_projects(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL,
    summary TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    tsv TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(summary, ''))
    ) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_engram_episodes_session ON engram_episodes(project_id, session_id, created_at DESC);
CREATE INDEX idx_engram_episodes_tsv ON engram_episodes USING GIN(tsv);

-- 6. Concurrent Task Coordination & Leases
CREATE TABLE engram_tasks (
    id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES engram_projects(id) ON DELETE CASCADE,
    queue TEXT NOT NULL DEFAULT 'default',
    title TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'PENDING' 
        CHECK (status IN ('PENDING', 'CLAIMED', 'COMPLETED', 'FAILED', 'BLOCKED')),
    priority INT NOT NULL DEFAULT 0,
    claimed_by TEXT,
    lease_id UUID,
    claimed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    claim_count INT NOT NULL DEFAULT 0,
    completed_at TIMESTAMPTZ,
    result JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, id)
);

CREATE INDEX idx_engram_tasks_claimable ON engram_tasks(project_id, queue, status, priority DESC, created_at ASC)
    WHERE status IN ('PENDING', 'CLAIMED');

-- 7. Task Handoff & Diagnostic Tombstones
CREATE TABLE engram_task_handoffs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    from_agent TEXT NOT NULL,
    to_agent TEXT NOT NULL,
    verdict TEXT NOT NULL,
    reason TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fk_task_handoff FOREIGN KEY (project_id, task_id)
        REFERENCES engram_tasks(project_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_engram_task_handoffs_task ON engram_task_handoffs(project_id, task_id, created_at DESC);
```

### Atomic Claim & Reaper Stealing Query (`FOR UPDATE SKIP LOCKED`)

```sql
WITH claimable AS (
    SELECT project_id, id, status, claimed_by, lease_id
    FROM engram_tasks
    WHERE project_id = $1 
      AND queue = $2
      AND (
          status = 'PENDING' 
          OR (status = 'CLAIMED' AND lease_expires_at < now())
      )
    ORDER BY priority DESC, created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE engram_tasks t
SET status = 'CLAIMED',
    claimed_by = $3,
    lease_id = gen_random_uuid(),
    claimed_at = now(),
    lease_expires_at = now() + ($4 || ' seconds')::interval,
    claim_count = t.claim_count + 1,
    updated_at = now()
FROM claimable c
WHERE t.project_id = c.project_id AND t.id = c.id
RETURNING 
    t.id, t.project_id, t.title, t.payload, t.status, 
    t.claimed_by, t.lease_id, t.lease_expires_at, t.claim_count,
    c.status AS previous_status, c.claimed_by AS previous_claimant;
```

---

## 5. Model Context Protocol (MCP) Tool Contracts

Engram exposes the following high-density, context-conserving MCP tools over Streamable HTTP and SSE:

### 1. `list_manifest` (Progressive Disclosure)
* **Description:** Retrieve a lightweight catalog of memories. Does not return content.
* **Input Schema:**
  ```json
  {
    "project_id": "engram",
    "scope": "optional string (e.g. project, architecture, convention)",
    "query": "optional string for fulltext filtering"
  }
  ```
* **Output Schema:**
  ```json
  {
    "manifest": [
      {
        "key": "architecture/outbox_pattern",
        "title": "Transactional Outbox Pattern in Auk",
        "digest": "Outbox table semantics, state transitions, and NATS publisher guarantees.",
        "token_count": 420,
        "scope": "architecture",
        "current_hash": "a1b2c3d4e5f6..."
      }
    ]
  }
  ```

### 2. `get_memories_batch` (Low-Latency Coarse Retrieval)
* **Description:** Fetch full content for one or more specific memory keys.
* **Input Schema:**
  ```json
  {
    "project_id": "engram",
    "keys": ["architecture/outbox_pattern", "conventions/go_errors"]
  }
  ```
* **Output Schema:**
  ```json
  {
    "memories": [
      {
        "key": "architecture/outbox_pattern",
        "title": "Transactional Outbox Pattern in Auk",
        "content": "... full markdown body ...",
        "current_hash": "a1b2c3...",
        "head_rev": 14,
        "token_count": 420,
        "updated_at": "2026-11-23T20:15:00Z"
      }
    ]
  }
  ```

### 3. `store_memory` (Optimistic Concurrency & Blame Mutation)
* **Description:** Store or mutate a memory with parent hash verification.
* **Input Schema:**
  ```json
  {
    "project_id": "engram",
    "key": "architecture/outbox_pattern",
    "title": "Transactional Outbox Pattern in Auk",
    "digest": "Updated outbox retry parameters",
    "scope": "architecture",
    "content": "... updated markdown body ...",
    "expected_parent_hash": "a1b2c3d4e5f6...",
    "rationale": "Tightened backoff multiplier to prevent NATS buffer bloat",
    "author": "Ivan",
    "agent_model": "claude-3-7-sonnet",
    "machine_id": "macbook-m3-max"
  }
  ```
* **Output Schema (Success):**
  ```json
  {
    "status": "STORED",
    "rev": 15,
    "hash": "f7e8d9c0...",
    "token_count": 435,
    "maintenance_notice": null
  }
  ```
* **Output Schema (Conflict - Non-Destructive Branching):**
  ```json
  {
    "status": "CONFLICT",
    "head_rev": 16,
    "head_hash": "99887766...",
    "expected_parent_hash": "a1b2c3d4...",
    "diff_unified": "@@ -12,4 +12,4 @@\n-timeout: 30s\n+timeout: 15s",
    "message": "Target memory was modified concurrently. Mutation preserved as divergent branch. Please resolve and resubmit with head_hash as parent."
  }
  ```

### 4. `squash_revisions` (Compaction Tool)
* **Description:** Consolidate micro-revisions into a single baseline snapshot.
* **Input Schema:**
  ```json
  {
    "project_id": "engram",
    "key": "architecture/outbox_pattern",
    "up_to_rev": 25,
    "rationale": "Milestone squash post v2.4 launch"
  }
  ```
* **Output Schema:**
  ```json
  {
    "status": "SQUASHED",
    "compacted_revisions_count": 24,
    "new_baseline_rev": 26,
    "retained_authors": ["Ivan", "claude-3-7-sonnet"]
  }
  ```

### 5. `claim_task` (Atomic Lease Claim)
* **Description:** Claim the next available task from a queue using `SKIP LOCKED`.
* **Input Schema:**
  ```json
  {
    "project_id": "engram",
    "queue": "migrations",
    "agent_id": "agent-worker-01",
    "lease_duration_seconds": 300
  }
  ```
* **Output Schema:**
  ```json
  {
    "status": "CLAIMED",
    "task": {
      "id": "task-mig-009",
      "title": "Migrate auk outbox schema",
      "payload": { "table": "auk_outbox" },
      "lease_id": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
      "lease_expires_at": "2026-11-24T05:05:00Z",
      "claim_count": 1
    },
    "reaper_handoff": null
  }
  ```

### 6. `renew_task_lease` (Heartbeat Extension)
* **Description:** Extend an active task lease to prevent reaper stealing during long operations.
* **Input Schema:**
  ```json
  {
    "project_id": "engram",
    "task_id": "task-mig-009",
    "lease_id": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
    "extend_seconds": 300
  }
  ```
* **Output Schema:**
  ```json
  {
    "status": "RENEWED",
    "new_lease_expires_at": "2026-11-24T05:10:00Z"
  }
  ```

### 7. `complete_task` (Task Finalization)
* **Description:** Mark a task completed and record result payload.
* **Input Schema:**
  ```json
  {
    "project_id": "engram",
    "task_id": "task-mig-009",
    "lease_id": "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
    "verdict": "COMPLETED",
    "result": { "status": "ok", "rows_migrated": 1420 }
  }
  ```

### 8. `log_episode` (Timeline Audit)
* **Description:** Append an execution trace event without polluting reference memory.
* **Input Schema:**
  ```json
  {
    "project_id": "engram",
    "session_id": "sess-20261124-01",
    "event_type": "build_failure",
    "actor": "claude-3-7-sonnet",
    "summary": "TypeScript compilation failed on missing type in auth package",
    "details": { "file": "src/auth/token.ts", "line": 42 }
  }
  ```

---

## 6. Alternatives Weighed & Justifications

1. **Flat Markdown in Git (`TODO.md`, `.serena/`):**
   - *Verdict: Rejected.* High token consumption on greedy directory reads, continuous git merge conflicts when multiple agents run concurrently, zero cross-machine coordination without git push/pull races.
2. **Valkey / Redis + JSON:**
   - *Verdict: Rejected.* Lacks native hybrid vector search, cannot perform complex multi-table SQL transactions, and lacks diff generation and audit blame chains.
3. **Embedded SQLite:**
   - *Verdict: Rejected.* Cannot coordinate across distinct physical machines (MacBook, remote VPS, CI runners) without complex replication layers (Litestream), and suffers write lock contention under concurrent agent loops.
4. **Third-Party SaaS (Mem0 Cloud, Pinecone, LangChain Hub):**
   - *Verdict: Rejected.* Violates data sovereignty, introduces recurring monthly SaaS billing, leaks proprietary codebase metadata to external providers, and lacks operational task coordination.
5. **PostgreSQL 18 + pgvector + tsvector (Self-Contained):**
   - *Verdict: Accepted.* Runs locally or on personal VPS, zero external data leakage, battle-tested `SKIP LOCKED` concurrency, sub-millisecond fulltext search, and full native transactional integrity.

---

## 7. Staged Implementation Roadmap

| Milestone | Focus | Deliverables |
| --- | --- | --- |
| Stage 1 | Schema & Database Harness | Postgres 18 Docker Compose, DDL, GIN/HNSW indexes, pgxpool & listener |
| Stage 2 | Core Memory & Content Blame | Progressive disclosure manifests, SHA-256 blame chain, difflib, squash |
| Stage 3 | Task Coordination & Reaper | SKIP LOCKED leases, heartbeat CAS, diagnostic handoff tombstones |
| Stage 4 | MCP Transport & CLI | Streamable HTTP/SSE MCP endpoints, `engram-cli` companion, Bearer auth |
| Stage 5 | Verification & E2E Validation | Concurrency stress test, multi-model fallback benchmark, recovery checks |

### Verification & Quality Gates
- **Runtime Vector Fallback:** Verify that vector search gracefully falls back to `ts_rank` fulltext search when vectors are absent, returning valid results without SQL errors.
- **Race Condition Immunity:** Run 10 concurrent agent workers claiming 50 tasks via `SKIP LOCKED`; assert zero duplicate claims and zero orphaned tasks.
- **Lease Reaper Autopsy:** Simulate agent crash by killing process during lease; verify task is reclaimed by next worker and `engram_task_handoffs` contains complete tombstone record.
- **Hash Blame Integrity:** Test concurrent writes to same memory key; assert divergent revision created with `is_conflict = true` and zero silent data loss.

