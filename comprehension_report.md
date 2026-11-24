# Stage 6 Comprehension Test Report: `docs/designs/engram.md`

## Paraphrase of the Specification

### What this proposes
Engram proposes a self-contained Go-based daemon (`engramd`) and companion developer CLI (`engram`) backed by PostgreSQL 18 (`pgvector` and `pg_trgm`) to serve as a centralized operational state plane, agentic memory store, and task coordinator for AI coding agents and human engineers over the Model Context Protocol (MCP) and HTTP/SSE. To eliminate prompt token bloat caused by dumping raw documentation, Engram introduces a two-tier progressive disclosure model where agents first query lightweight skeletal manifests (keys, digests, token counts) and only hydrate full memory content on demand. Memory updates are recorded in an append-only revision ledger using SHA-256 hash chains, preserving concurrent diverging edits as non-destructive conflict branches rather than overwriting data. For cross-machine multi-agent execution, it coordinates work through atomic task queues utilizing PostgreSQL row-level locks (`FOR UPDATE SKIP LOCKED`), dynamic lease renewal heartbeats, and automatic crash diagnostics logged by a system lease reaper when tasks time out.

### How the existing system works, per this document
According to the design document, current AI-assisted workflows track architecture rules, project guidelines, and task status using flat Markdown files committed directly to Git (such as `TODO.md`, `.serena/memories/*.md`, and `checamos-docs/`) or stored in local directories. AI agents routinely read and dump these entire files into their context on every prompt, consuming 40% to 80% of available tokens on static reference prose and causing attention drift. When multiple agents or developers run concurrently across local machines, remote VPS environments (such as Contabo), or parallel Git branches, concurrent edits to these Markdown files frequently cause Git merge clobbering or silent overwrites. Furthermore, there is no real-time cross-machine visibility into in-flight task execution, and raw Markdown files fail to provide cryptographic blame or provenance tracking to identify which agent model, user, or machine introduced a change or why.

### Paraphrase of the Detailed Implementation Plan (Lines 385–460)
The detailed plan specifies a 5-stage sequential rollout covering database infrastructure, queries, core business logic, transport protocol servers, and executable binaries:
- **Stage 1 (Database & Schema):** Provisions PostgreSQL 18 via `docker-compose.yml` on port 5433, writes migration `00001_initial_schema.sql` establishing the relational tables and HNSW/GIN indexes, configures `sqlc.yaml`, sets up the `pgxpool.Pool` connection manager, automated migration runner, and a dedicated `pgx.Conn` PostgreSQL event listener worker.
- **Stage 2 (SQL Queries & Generation):** Implements `.sql` query files for projects, memories, revisions, tasks, handoffs, and episodes, generating type-safe Go structs and query methods using `sqlc`.
- **Stage 3 (Core Domain Services):** Implements memory hashing and diffing in `blame.go`, manifest generation and optimistic concurrency in `service.go`, handoff validation (<150 words, verdict enum, evidence) in `handoff.go`, task lease management and reaper autopsy creation in `state/service.go`, and narrative session logging in `episodes/service.go`.
- **Stage 4 (Middleware & MCP Protocol):** Introduces Fiber Bearer authentication middleware (supporting headers and `?token=` query parameters), RFC 7807 Problem Details error handling via Huma v2, and registers JSON-RPC MCP tools and SSE streaming handlers.
- **Stage 5 (Daemon, CLI & Tests):** Assembles the `engramd` daemon entrypoint and the `engram` developer CLI, accompanied by unit and integration test suites covering diffing, hash chains, handoffs, lease stealing, and MCP protocol handling.

---

## Comprehension Findings

### 1. Undefined Terms & Acronyms

#### Finding 1.1: Undefined internal repo paths and project-specific identifiers
- **Severity**: Nit
- **Affected Scope**: Line 13, Line 50
- **Discrepancy Description & Rationale**:
  The document references `.serena/memories/*.md`, `checamos-docs/` (line 13), and `project_id: "birdple-v2"` (line 50) without defining them. While experienced members of the team may recognize them as existing repository conventions or external tools, a naive reader cannot determine whether `.serena` is an internal tool, a third-party framework, or a legacy configuration directory.
  - *Recommendation*: Add a brief parenthetical clarifying that these are examples of legacy Git-tracked documentation directories and project identifiers.

#### Finding 1.2: Undefined token estimation algorithm (`uax29`)
- **Severity**: Nit
- **Affected Scope**: Line 426 (`internal/core/memories/service.go`)
- **Discrepancy Description & Rationale**:
  The document mentions "token estimation via `uax29`". Unicode Standard Annex #29 (UAX #29) defines Unicode text segmentation algorithms for grapheme clusters, words, and sentences; it is not a subword tokenizer (like BPE, tiktoken, or SentencePiece used by LLMs). A naive reader is left confused whether Engram is counting linguistic words or estimating LLM tokens with a heuristic multiplier.
  - *Recommendation*: Define how UAX #29 is adapted for token counting (e.g., word segmentation with an empirical token multiplier) or clarify why a standard BPE tokenizer is not used.

#### Finding 1.3: Unexplained third-party Go dependencies
- **Severity**: Nit
- **Affected Scope**: Lines 80, 424, 439
- **Discrepancy Description & Rationale**:
  The design references `Fiber v2`, `Huma v2` (lines 80, 439), and `difflib` (line 424) without introductory context. While standard in Go ecosystems, a fresh reader is not informed whether `difflib` is an internal utility package or `github.com/pmezard/go-difflib`, or what role Huma plays on top of Fiber.
  - *Recommendation*: Explicitly state the package import paths and roles (e.g., `gofiber/fiber/v2` HTTP web router, `danielgtaylor/huma/v2` OpenAPI 3.1 framework, `pmezard/go-difflib` unified diff generator).

---

### 2. Ambiguous Mechanics & Missing Workflows

#### Finding 2.1: Missing MCP tools for task creation, handoffs, and search
- **Severity**: Blocker
- **Affected Scope**: Lines 442–444 (`internal/mcp/tools.go`), Lines 218–257
- **Discrepancy Description & Rationale**:
  Stage 4 explicitly enumerates all registered MCP tools: `list_manifest`, `get_memories_batch`, `store_memory`, `blame_memory`, `squash_revisions`, `claim_task`, `renew_task_lease`, `complete_task`, and `log_episode`.
  Several essential operational capabilities described elsewhere in the document are completely absent from this tool list:
  1. **Task Enqueuing / Creation**: There is no `create_task` or `enqueue_task` tool. Neither an AI agent nor an external orchestrator connecting over MCP has a way to insert tasks into `engram_tasks`.
  2. **Recording Handoffs**: Stage 3 implements `internal/core/state/handoff.go` to enforce a strict handoff contract, and Stage 5 mentions CLI command `handoff`, but no MCP tool (`record_handoff`) exists for an agent to submit a task handoff.
  3. **Task Failure / Blocking**: The status check in `engram_tasks` allows `'PENDING', 'CLAIMED', 'COMPLETED', 'FAILED', 'BLOCKED'`, but the toolset only provides `complete_task`. An agent cannot mark a task as failed or blocked via MCP.
  4. **Search Capabilities**: While Stage 2 defines hybrid search in `memories.sql` and fulltext search in `episodes.sql`, no MCP search tool (`search_memories`, `search_episodes`) is exposed to agents.
  - *Recommendation*: Add `create_task`, `fail_task`, `record_handoff`, and `search_memories` to `internal/mcp/tools.go`.

#### Finding 2.2: Undefined conflict resolution and reconciliation protocol
- **Severity**: Major
- **Affected Scope**: Lines 53–57 (CUJ 2), Lines 301–307 (Sequence 2)
- **Discrepancy Description & Rationale**:
  When Agent B encounters a collision (`expected_parent_hash` does not match), Engram records a divergent revision (`is_conflict: true`) and returns `status: "CONFLICT"` with a unified diff between the current head and Agent B's submission. However, the document does not explain how the conflict is actually resolved:
  - Does Agent B call `store_memory` again specifying `expected_parent_hash` set to the new head hash?
  - Does the resolution overwrite the conflicted revision, or does it create a new non-conflicted revision?
  - What happens to the branched revision marked `is_conflict: true`? Does it remain in the revision history indefinitely, and will `list_manifest` or `get_memories_batch` ever expose or acknowledge it?
  - *Recommendation*: Document the complete reconciliation workflow and API contract, specifying how agents finalize a merge and whether a dedicated `resolve_conflict` action or standard `store_memory` call is used.

#### Finding 2.3: Undefined database trigger mechanism for `NOTIFY engram_events`
- **Severity**: Major
- **Affected Scope**: Lines 88, 107, 340–348 (Section 4), Line 401
- **Discrepancy Description & Rationale**:
  Section 4 states: *"When a database mutation occurs, a PostgreSQL trigger fires `NOTIFY engram_events, payload`. The dedicated worker decodes the payload and broadcasts it across matching channels in-memory."*
  However:
  - The PostgreSQL DDL (Section 2) does not define any `CREATE TRIGGER` statements or PL/pgSQL trigger functions.
  - The document does not specify which tables fire triggers (only `engram_tasks`? `engram_memories`?), which mutations trigger them (INSERT, UPDATE, DELETE), or what JSON schema `payload` must adhere to.
  - Stage 1 and Stage 2 omit trigger definitions from their file deliverables.
  - *Recommendation*: Define the PL/pgSQL trigger function and `CREATE TRIGGER` DDL in `migrations/00001_initial_schema.sql`, detailing the JSON payload format (e.g., `{ "table": "engram_tasks", "action": "UPDATE", "id": "...", "project_id": "..." }`).

#### Finding 2.4: Infinite crash loops and missing dead-letter queue behavior
- **Severity**: Major
- **Affected Scope**: Lines 58–62 (CUJ 3), Lines 230, 310–338 (Sequence 3)
- **Discrepancy Description & Rationale**:
  The task schema includes `claim_count INT NOT NULL DEFAULT 0`, and the lease reaper automatically reassigns an expired task to the next claiming worker along with an autopsy tombstone. However, the document never defines a maximum claim threshold (max retries). If a task contains a deterministic bug that crashes any worker attempting it, subsequent workers will repeatedly steal and crash on the task indefinitely.
  - *Recommendation*: Specify a max retry threshold (e.g., `max_claims = 3`) after which the atomic claim query automatically marks the task `status = 'FAILED'` or `'BLOCKED'` rather than re-granting the lease.

---

### 3. Mental Model Gaps & Architectural Discrepancies

#### Finding 3.1: Missing `content` column in `engram_revisions` prevents content recovery and conflict preservation
- **Severity**: Blocker
- **Affected Scope**: Lines 176–194 (DDL for `engram_revisions`), Lines 298–306 (Sequence 2)
- **Discrepancy Description & Rationale**:
  In Section 2, the `engram_revisions` table schema is defined as:
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
      diff TEXT NOT NULL DEFAULT '',
      rationale TEXT NOT NULL DEFAULT '',
      is_conflict BOOLEAN NOT NULL DEFAULT false,
      is_squashed BOOLEAN NOT NULL DEFAULT false,
      squashed_into_rev INT,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      PRIMARY KEY (project_id, key, rev), ...
  );
  ```
  Notice that `engram_revisions` contains a `diff` column, but **no `content` column**. Full content is only stored in `engram_memories.content` (which only stores the latest HEAD).
  This causes two critical breakdowns in the system's mental model:
  1. In Sequence 2, when Agent B submits content `C2` resulting in a conflict, Engram records revision 3 with `is_conflict: true`. But where is `C2` stored? If `engram_memories` holds `C1`, and `engram_revisions` only holds a diff, Agent B's proposed full text `C2` cannot be recovered unless `diff` is guaranteed to be reversible or stores full content for conflicts.
  2. For historical revisions, if full content is not stored in revisions, reconstructing revision $N$ requires applying reverse diffs from the current HEAD backwards (or forward diffs from revision 1). But revision 1 has no previous revision to diff against; does revision 1 store full content in `diff`? Furthermore, if revisions are squashed (`is_squashed = true`), intermediate diff chains are interrupted, making reconstruction mathematically impossible.
  - *Recommendation*: Either add `content TEXT NOT NULL` to `engram_revisions`, or explicitly specify how full revision content is reconstructed from diffs and where conflicting proposed content is persisted.

#### Finding 3.2: Rigid vector column dimensions contradict stated design goal
- **Severity**: Major
- **Affected Scope**: Line 112, Lines 156–174 (`engram_embeddings`)
- **Discrepancy Description & Rationale**:
  Section 2 introduces the DDL with the explicit goal: *"To eliminate vector dimension rigidity... the relational schema in PostgreSQL 18 is partitioned as follows"*.
  However, the table `engram_embeddings` hardcodes two specific vector columns:
  ```sql
  embedding_1536 VECTOR(1536),
  embedding_768 VECTOR(768),
  ```
  Hardcoding explicit columns of 1536 and 768 dimensions is the antithesis of eliminating dimension rigidity. Modern state-of-the-art embedding models use various dimensions (e.g., 384 for `all-MiniLM-L6-v2`, 1024 for `bge-large`, 3072 for OpenAI `text-embedding-3-large`). The schema provides no mechanism to support or index these dimensions without modifying the DDL and adding new columns.
  - *Recommendation*: Clarify whether Engram strictly constrains supported models to 768-d and 1536-d, or describe how dynamic dimensions are accommodated.

#### Finding 3.3: Undefined canonical serialization and hash chain formula
- **Severity**: Major
- **Affected Scope**: Line 29 (G2), Lines 180–181, Line 424
- **Discrepancy Description & Rationale**:
  The document mandates a "verifiable SHA-256 parent hash chain" and optimistic checking via `expected_parent_hash`. However, the exact hashing formula is never specified:
  - Is `hash = SHA256(content)`?
  - Or is it a cryptographic chain: `hash = SHA256(parent_hash + ":" + content)`?
  - Are `title`, `key`, `metadata`, `author`, or `diff` included in the hash preimage?
  - What is the initial `parent_hash` for revision 1 (e.g., empty string `""` or 64 zeros)?
  Without a deterministic canonical serialization standard, clients and third-party tools cannot independently verify the hash chain.
  - *Recommendation*: Define the exact canonical string representation and hashing formula (e.g., `SHA256(parent_hash || "\n" || key || "\n" || content)`).

#### Finding 3.4: Contradictory semantics between reaper autopsy and task claim status
- **Severity**: Major
- **Affected Scope**: Line 61 (CUJ 3), Lines 248, 334–338 (Sequence 3), Line 428
- **Discrepancy Description & Rationale**:
  In CUJ 3 and Sequence 3, when Worker B acquires an expired task previously held by crashed Worker A, Engram inserts an autopsy handoff record into `engram_task_handoffs` with `from_agent: "system/lease-reaper"` and `verdict: "BLOCKED"`.
  However:
  - In Stage 3 (line 428), handoff verdicts are restricted to: `PASS`, `FAIL`, `BLOCKED`, `NEEDS_REVIEW`, `HANDOFF`. There is no verdict for `TIMEOUT` or `EXPIRED`.
  - Marking the autopsy handoff as `verdict: "BLOCKED"` while simultaneously returning the task to Worker B in `status: "CLAIMED"` creates a severe mental model contradiction: the task is actively claimed and runnable, yet its latest handoff states that it is blocked.
  - *Recommendation*: Introduce a distinct verdict enum value such as `LEASE_EXPIRED` or `TIMEOUT`, or clarify why the reaper verdict is categorized as `BLOCKED`.

#### Finding 3.5: Undefined squashing semantics and its impact on cryptographic provenance
- **Severity**: Major
- **Affected Scope**: Lines 188–189, Line 411, Line 426
- **Discrepancy Description & Rationale**:
  The document introduces revision squashing (`is_squashed BOOLEAN`, `squashed_into_rev INT`, and MCP tool `squash_revisions`). However, Goal G2 mandates an "append-only, cryptographic blame trail" with a "verifiable SHA-256 parent hash chain".
  The document fails to explain:
  - When revisions 2 and 3 are squashed into revision 4, what does revision 4's `parent_hash` point to? If it points to revision 1, the SHA-256 chain has been rewritten; if it points to revision 3, how does squashing save space or simplify history?
  - What happens to the diffs of squashed revisions?
  - How does squashing interact with the append-only audit requirement?
  - *Recommendation*: Provide a dedicated subsection explaining the exact mechanics of non-destructive squashing and how provenance verification behaves when traversing squashed revisions.

---

## Summary Matrix

| Finding ID | Severity | Category | Area | Core Issue |
| :--- | :--- | :--- | :--- | :--- |
| **3.1** | **Blocker** | Mental Model Gap | Data Model / History | Missing `content` column in `engram_revisions` prevents storing conflict branches and historical hydration |
| **2.1** | **Blocker** | Missing Workflow | MCP Protocol | Missing MCP tools for task creation (`create_task`), handoffs (`record_handoff`), and search (`search_memories`) |
| **2.2** | **Major** | Missing Workflow | Concurrency | No documented workflow or API contract for reconciling optimistic concurrency conflicts |
| **2.3** | **Major** | Missing Workflow | Database / SSE | Missing PostgreSQL trigger definitions and payload schema for `NOTIFY engram_events` |
| **2.4** | **Major** | Missing Workflow | Task State Plane | Missing max retry / dead-letter threshold to prevent infinite worker crash loops on poisoned tasks |
| **3.2** | **Major** | Mental Model Gap | Relational Schema | Hardcoding `embedding_1536` and `embedding_768` contradicts stated goal of eliminating dimension rigidity |
| **3.3** | **Major** | Mental Model Gap | Cryptography / Blame | Unspecified canonical serialization and hash chain formula for SHA-256 revisions |
| **3.4** | **Major** | Mental Model Gap | Task State Plane | Contradictory `verdict: "BLOCKED"` handoff emitted on an active `CLAIMED` task acquisition |
| **3.5** | **Major** | Mental Model Gap | Provenance | Undefined mechanics of revision squashing versus append-only SHA-256 hash chains |
| **1.1** | **Nit** | Terminology | Overview / CUJ | Unexplained internal repo paths (`.serena`, `checamos-docs`, `birdple-v2`) |
| **1.2** | **Nit** | Terminology | Domain Services | Unclear usage of `uax29` for LLM token counting |
| **1.3** | **Nit** | Terminology | Architecture | Missing definitions and package paths for `Fiber v2`, `Huma v2`, and `difflib` |
