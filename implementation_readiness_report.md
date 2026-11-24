# Goldfish Review Report: Stage 9 Implementation Readiness Test

**Target:** `docs/designs/engram.md` (Lines 385-460)

## Reviewer Status

| Reviewer | Status |
| --- | --- |
| `egm-implementation-readiness` | completed |
| `egm-codebase-details` | completed |
| `egm-style` | completed |

## Summary of Findings

- 🔴 **Blockers:** 4
- 🟡 **Major:** 5
- 🟢 **Nits:** 2

---

## Prioritized Findings (Blockers -> Major -> Nits)

### 1. Unresolved Architectural Contradiction: Ephemeral LISTEN/NOTIFY vs. Durable Outbox Stream - Severity: Blocker
* **Reviewer Lens:** `egm-implementation-readiness`
* **Affected Scope:** Lines 394 (item 2), 401-402 (item 6), 444-445 (item 23), 450 (item 24)
* **Discrepancy Description**:
  Stage 7 Critic Review (Finding 2) and Stage 8 Intermediate Zoom Out Review (Finding 2) demonstrated that PostgreSQL `NOTIFY` is fatally limited by a hard-coded 8,000-byte payload limit (aborting mutating transactions exceeding 8KB) and is strictly ephemeral (dropping events during client disconnections or network flaps with zero replay capability). The agreed resolution was adopting a durable Transactional Outbox table (`engram_events`).
  However, the Detailed Implementation Plan (lines 385-460) still prescribes the obsolete ephemeral design:
  - Line 394 (`migrations/00001_initial_schema.sql`) does not enumerate an `engram_events` table.
  - Line 401-402 (`listener.go`) explicitly directs the implementer to build an isolated background worker running `LISTEN engram_events` fanning out directly to an in-memory channel registry.
  - Line 445 (`server.go`) specifies bridging notifications to an in-memory listener hub rather than reading from a persistent replay buffer keyed to `Last-Event-ID`.
  - Stage 2 (`internal/db/queries/`) lacks query files for inserting outbox events and fetching missed events by sequence ID.
  An implementer following this plan is left in a direct contradiction between the review mandates and the file-by-file specification.
* **Rationale & Recommendation**:
  Update Stage 1 and Stage 2 to align with the durable outbox architecture:
  1. In `migrations/00001_initial_schema.sql`, add `engram_events` (`id BIGSERIAL PRIMARY KEY, project_id TEXT NOT NULL REFERENCES engram_projects(id) ON DELETE CASCADE, topic TEXT NOT NULL, payload JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()`).
  2. Add `internal/db/queries/events.sql` containing `InsertEvent` and `GetEventsAfter(project_id, after_id, limit)`.
  3. Specify in `listener.go` that `LISTEN engram_events` is used purely as a wake-up ping carrying monotonic `event_id`, while `server.go` reads from `engram_events` to support `Last-Event-ID` SSE recovery.

---

### 2. Missing Core Operational Workflows in MCP Tools and Domain Services - Severity: Blocker
* **Reviewer Lens:** `egm-implementation-readiness`
* **Affected Scope:** Lines 406-417 (Stage 2), 423-433 (Stage 3), 442-443 (Stage 4)
* **Discrepancy Description**:
  Line 443 lists the registered MCP tools: `list_manifest`, `get_memories_batch`, `store_memory`, `blame_memory`, `squash_revisions`, `claim_task`, `renew_task_lease`, `complete_task`, `log_episode`.
  Several critical operational workflows defined in the requirements and schema are completely omitted from the MCP tool registry, queries, and service layers:
  1. **Task Creation (`create_task` / `enqueue_task`)**: There is no tool or API for an agent or human to create a task in `engram_tasks`. Without an ingestion tool, the task queue cannot be populated.
  2. **Manual Task Handoff (`record_handoff` / `handoff_task`)**: While automated reaper handoffs are handled internally, manual agent-to-agent collaborative handoffs (`from_agent`, `to_agent`, `verdict`, `reason`, `metadata`) are defined in Section 2 (lines 241-257) and Stage 3 (line 428), but no MCP tool exposes this capability to agents.
  3. **Memory Search (`search_memories`)**: Goal G6 (line 33) and line 409 define dual-tier hybrid ranking (vector cosine fallback to `ts_rank`), but no MCP search tool exists in line 443. The only memory read operations are skeletal listing and exact key batch retrieval.
  4. **Episode Retrieval / Search (`list_episodes` / `search_episodes`)**: `engram_episodes` has fulltext search indexing (`idx_engram_episodes_tsv`), and line 417 specifies "session-scoped fulltext search", yet line 443 only lists `log_episode`. Episodic persistence is rendered write-only because agents have no tool to query past episodes.
* **Rationale & Recommendation**:
  Enumerate and specify the missing tools in Stage 4 (`internal/mcp/tools.go`), their corresponding SQL queries in Stage 2 (`internal/db/queries/`), and service orchestration methods in Stage 3:
  - `create_task(project_id, queue, title, payload, priority)`
  - `record_handoff(project_id, task_id, to_agent, verdict, reason, metadata)`
  - `search_memories(project_id, query, scope, limit)`
  - `query_episodes(project_id, session_id, query, limit)`

---

### 3. Ambiguous Conflict Handling & Primary Key Collision on Branch Revisions - Severity: Blocker
* **Reviewer Lens:** `egm-implementation-readiness` / `egm-codebase-details`
* **Affected Scope:** Lines 408-411 (items 8 & 9), 425-426 (item 15)
* **Discrepancy Description**:
  The implementation plan specifies:
  - Line 409: "optimistic hash upserts with conflict branching flags"
  - Line 411: "parent hash validation, unified diff retrieval, and non-destructive squashing (`is_squashed = true`, `squashed_into_rev = $rev`)"
  - Line 426: "optimistic hash checking (`expected_parent_hash`), non-destructive conflict branching"
  In Section 2 (lines 176-194), `engram_revisions` has `PRIMARY KEY (project_id, key, rev)`.
  If revision 2 is HEAD, and two concurrent writes arrive, attempting to write both as revision 3 (or incrementing rev while leaving `engram_memories.head_rev` at 2) causes either a PostgreSQL unique constraint violation or an unindexed, unaddressable branch fork (as identified in Stage 7 Critic Finding 1).
  Stage 8 Intermediate Zoom Out (Finding 1) recommended replacing speculative DAG branching with standard Optimistic Concurrency Control (OCC returning HTTP 409 Conflict with diff and current HEAD).
  The implementation plan leaves the exact implementation unresolved:
  - If Git-like branching: The plan fails to specify the modified DDL, how revision numbers are allocated to prevent PK collisions, or how divergent heads are queried.
  - If OCC: The plan fails to specify the rejection error schema, HTTP/JSON-RPC status codes, and rejection diff generation contracts.
* **Rationale & Recommendation**:
  Replace the ambiguous "conflict branching flags" in Stage 2 and Stage 3 with strict Optimistic Concurrency Control:
  1. Specify that `service.go` performs OCC validation: if `expected_parent_hash != current_hash`, the daemon rejects the write with error code `ERR_STALE_REVISION` (HTTP 409 / JSON-RPC error -32001) containing `{head_rev, head_hash, current_content, diff_unified}`.
  2. If append-only rejection logging is retained for audit, direct it to an explicit `engram_mutation_rejections` table.
  3. Maintain `engram_revisions` strictly linear and monotonic, incrementing `rev` only on accepted mutations.

---

### 4. Unspecified Vector Embedding Ingestion & Pipeline Orchestration - Severity: Blocker
* **Reviewer Lens:** `egm-implementation-readiness` / `egm-codebase-details`
* **Affected Scope:** Lines 394 (item 2), 396 (item 3), 408-409 (item 8), 425-426 (item 15)
* **Discrepancy Description**:
  The schema provisions `engram_embeddings` with `embedding_1536 VECTOR(1536)` and `embedding_768 VECTOR(768)` alongside HNSW indexes (line 394), and line 409 specifies "dual-tier hybrid ranking (vector cosine distance fallback to `ts_rank`)".
  However, nowhere in Stage 3 (`internal/core/memories/service.go`) or Stage 4 is there any specification of how embeddings are generated or ingested:
  1. Does `engramd` call an external embedding provider (OpenAI `text-embedding-3-small`, Ollama, local model), or does the calling agent provide the embedding vector in `store_memory`?
  2. If generated by `engramd`: What package/client performs the HTTP call? Where are provider credentials configured? What file contains the embedding client? What is the failure mode if the embedding API times out (does the memory write abort, or succeed with asynchronous background embedding generation)?
  3. If provided by the client: Why does the MCP tool schema omit vector parameters?
  An implementer cannot write `internal/core/memories/service.go` or `internal/db/queries/memories.sql` without making fundamental architectural decisions regarding vector ingestion.
* **Rationale & Recommendation**:
  Explicitly specify the vector pipeline in the plan:
  1. Add `internal/platform/embedding/client.go` to Stage 3 or 4, defining an `Embedder` interface with implementations for external providers (e.g., OpenAI API) and a no-op fallback.
  2. Specify whether embedding generation occurs synchronously during `store_memory` or asynchronously via an outbox event.
  3. Define fallback behavior: if no embedding provider is configured, vector insertion is skipped and retrieval queries transparently fall back to fulltext `tsvector`.

---

### 5. Omission of Foundational Go Project Files & Configuration Engine - Severity: Major
* **Reviewer Lens:** `egm-codebase-details`
* **Affected Scope:** Lines 389-403 (Stage 1), 449-452 (Stage 5)
* **Discrepancy Description**:
  Stage 1 jumps directly to `docker-compose.yml`, `migrations/`, and `sqlc.yaml`. Several foundational project files are missing:
  1. **`go.mod` & `go.sum`**: No Go module definition is listed. The Go version (e.g., Go 1.23) and module path (e.g., `github.com/ivan/engram`) are unspecified.
  2. **Configuration Package (`internal/platform/config/config.go`)**: Stage 5 notes that `cmd/engramd/main.go` "parses configuration", but no configuration module or struct definition is enumerated in Stage 1, 4, or 5. Required environment variables (`DATABASE_URL`, `ENGRAM_PORT`, `ENGRAM_AUTH_TOKEN`, `DEFAULT_LEASE_TTL`, `LOG_LEVEL`) and default fallbacks are absent.
  3. **Environment Template (`.env.example`)**: No environment file template is enumerated to document configuration flags for local or container deployment.
* **Rationale & Recommendation**:
  Add these files to Stage 1:
  - `go.mod`: Go 1.23+ with module path and core dependencies (`github.com/jackc/pgx/v5`, `github.com/gofiber/fiber/v2`, `github.com/danielgtaylor/huma/v2`, `github.com/pgvector/pgvector-go`, `github.com/pmezard/go-difflib`).
  - `internal/platform/config/config.go`: typed configuration struct with environment variable tags and documented defaults.
  - `.env.example`: template file containing all configuration options.

---

### 6. Unspecified Cryptographic Blame Serialization and Hashing Canonicalization - Severity: Major
* **Reviewer Lens:** `egm-implementation-readiness` / `egm-codebase-details`
* **Affected Scope:** Lines 423-424 (item 14)
* **Discrepancy Description**:
  Line 424 specifies `internal/core/memories/blame.go`: "Implements SHA-256 hash chaining, strict LF line ending normalization, unified diff generation using `difflib`, and unbroken cryptographic chain verification."
  The specification does not define the canonical preimage format for the SHA-256 hash calculation:
  - Is the hash computed over `normalizeLF(content)` alone?
  - Or is it computed over a composite header including parent hash, author, agent model, machine ID, and content?
  - What is the canonical representation of the root revision (rev 1) parent hash (`""`, `"GENESIS"`, or 64 zero hex digits)?
  If two implementers or future tooling format the preimage differently, hash chain verification will fail, invalidating cryptographic provenance.
* **Rationale & Recommendation**:
  Define the canonical preimage specification in `blame.go`:
  ```go
  canonicalPreimage := fmt.Sprintf("parent:%s\nkey:%s\nauthor:%s\nmodel:%s\nmachine:%s\ncontent:\n%s", 
      parentHash, key, author, agentModel, machineId, normalizeLF(content))
  hash := fmt.Sprintf("%x", sha256.Sum256([]byte(canonicalPreimage)))
  ```
  Explicitly mandate that root revision 1 uses `parent_hash = ""` (empty string).

---

### 7. Ambiguous Token Estimation Algorithm & Contract in Memory Service - Severity: Major
* **Reviewer Lens:** `egm-implementation-readiness`
* **Affected Scope:** Line 426 (item 15)
* **Discrepancy Description**:
  Line 426 specifies: "token estimation via `uax29`".
  As highlighted in Stage 6 Comprehension Test (Finding 1.2), Unicode Standard Annex #29 defines text segmentation into graphemes, words, and sentences, not BPE subword tokenization used by LLMs.
  The plan gives no guidance on how UAX #29 is adapted for token counting:
  - Multiply word count by a fixed coefficient (e.g. 1.33)?
  - Count character length divided by 4?
  - Use an actual Go BPE tokenizer package (e.g., `github.com/pkoukk/tiktoken-go`)?
  Leaving this unspecified allows arbitrary token estimation implementations that risk violating the <1,500 token budget constraint for manifests (Goal G1).
* **Rationale & Recommendation**:
  Explicitly define the token estimation strategy in `internal/core/memories/service.go`:
  Either standardize on an exact heuristic formula:
  `token_count = int(math.Ceil(float64(uax29_word_count) * 1.35)) + (whitespace_count / 4)`
  or specify a BPE tokenizer dependency (`github.com/pkoukk/tiktoken-go` with `cl100k_base` encoding).

---

### 8. CLI Command Architecture & Communication Protocol Underspecified - Severity: Major
* **Reviewer Lens:** `egm-codebase-details`
* **Affected Scope:** Lines 451-452 (item 25)
* **Discrepancy Description**:
  Line 452 describes `cmd/engram/main.go` as implementing 8 commands (`manifest`, `recall`, `search`, `store`, `blame`, `claim`, `handoff`, `squash`) communicating "directly via JSON-RPC over HTTP with the daemon".
  1. **Monolithic Organization**: Consolidating 8 CLI commands, argument parsing, HTTP transport, and terminal output formatting into a single file violates idiomatic Go CLI patterns.
  2. **CLI Framework & Flags**: The plan does not specify the CLI library (e.g., `spf13/cobra` vs standard `flag`) or global flags (`--server` / `ENGRAM_SERVER_URL`, `--token` / `ENGRAM_TOKEN`, `--project` / `-p`, `--json`).
  3. **Communication Endpoint**: The plan does not specify which HTTP endpoint the CLI targets (`POST /mcp/message`, `/rpc`, or REST endpoints).
* **Rationale & Recommendation**:
  Refactor the CLI layout in Stage 5:
  - `cmd/engram/main.go`: Entrypoint initializing root command and global flags using `github.com/spf13/cobra`.
  - `cmd/engram/commands/`: Modular command definitions (`manifest.go`, `recall.go`, `search.go`, `store.go`, `blame.go`, `tasks.go`, `squash.go`).
  - `internal/client/client.go`: Shared typed HTTP/JSON-RPC client used by the CLI.

---

### 9. Underspecified Integration Test Execution Strategy & Database Harness - Severity: Major
* **Reviewer Lens:** `egm-codebase-details`
* **Affected Scope:** Lines 453-460 (items 26-29)
* **Discrepancy Description**:
  Stage 5 enumerates four test suites labeled "Unit and integration tests asserting...".
  Integration testing of `SKIP LOCKED` task stealing, trigram queries, and HNSW cosine searches requires a live PostgreSQL 18 instance with extensions enabled.
  The plan lacks instructions on:
  1. How tests connect to PostgreSQL: Expecting an existing instance via `ENGRAM_TEST_DATABASE_URL` (e.g. port 5433 from `docker-compose.yml`) vs. spinning up dynamic containers via `testcontainers-go`.
  2. Test isolation strategy: Rolling back transactions per test vs. provisioning per-test schemas (`CREATE SCHEMA test_uuid`).
  Without this specification, tests will either mock out the database (failing to validate real PostgreSQL locking and indexing semantics) or collide when run concurrently.
* **Rationale & Recommendation**:
  Add `internal/platform/testutil/testdb.go` to Stage 5:
  - Reads `ENGRAM_TEST_DATABASE_URL` (defaulting to `postgres://engram:engram@localhost:5433/engram_test?sslmode=disable`).
  - Executes sequential migrations on setup and isolates test cases via per-test database transactions or temporary schemas.
  - Clarifies that unit tests (`blame_test.go`, `handoff_test.go`, `auth_test.go`) run purely in-memory without database dependencies.

---

### 10. Inconsistent Handoff Validation Field Terminology - Severity: Nit
* **Reviewer Lens:** `egm-style` / `egm-implementation-readiness`
* **Affected Scope:** Line 428 (item 16) vs. Lines 248-249
* **Discrepancy Description**:
  Line 428 states that `internal/core/state/handoff.go` enforces "<150 words summary, valid verdict (`PASS`, `FAIL`, `BLOCKED`, `NEEDS_REVIEW`, `HANDOFF`), and non-empty reproducible evidence."
  However, in the schema DDL (lines 248-249), the column is named `reason TEXT NOT NULL`, not `summary`, and there is no dedicated `evidence` column (only `metadata JSONB`).
* **Rationale & Recommendation**:
  Harmonize the terminology: Clarify that the <150 words constraint applies to `reason` (or rename the column in DDL to `summary`), and specify that reproducible evidence is enforced as a required key in `metadata` (`metadata->>'evidence' != ''`).

---

### 11. Missing Canonical Go Module Import Paths for Third-Party Libraries - Severity: Nit
* **Reviewer Lens:** `egm-codebase-details` / `egm-style`
* **Affected Scope:** Lines 396, 424, 437, 439
* **Discrepancy Description**:
  While line 396 explicitly specifies `github.com/pgvector/pgvector-go.Vector`, lines 424, 437, and 439 reference third-party packages by shorthand names (`difflib`, `Fiber`, `Huma v2`) without standard Go module paths.
* **Rationale & Recommendation**:
  Enumerate the exact canonical Go import paths:
  - `difflib`: `github.com/pmezard/go-difflib/difflib`
  - `Fiber`: `github.com/gofiber/fiber/v2`
  - `Huma`: `github.com/danielgtaylor/huma/v2`
  - `pgx`: `github.com/jackc/pgx/v5`
