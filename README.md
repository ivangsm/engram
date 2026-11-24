# Engram (`engram`)

## Architectural Vision
Engram is a high-performance agentic memory service, episodic/semantic persistence engine, and multi-machine operational state plane written in Go (Go 1.24+) and backed by PostgreSQL 18 with `pgvector` and `pg_trgm`. It exposes standard Model Context Protocol (MCP) streaming over HTTP/SSE and provides a developer CLI to eliminate plain-text context dumping, merge conflicts, and uncoordinated state in autonomous AI agent workflows.

## Subsystem Navigation
- [`cmd`](./cmd/README.md): Executable binaries for the Engram background daemon (`engramd`) and the developer CLI tool (`engram`).
- [`internal`](./internal/README.md): Core domain logic, type-safe persistence layer (`sqlc`), MCP protocol handlers, and infrastructure primitives.
- [`migrations`](./migrations/README.md): Canonical PostgreSQL schema migrations defining tables, indexes, constraints, and audit outbox tables.

## Global Invariants
- **Progressive Disclosure**: Memory listings return compact skeletal manifests (`key`, `digest`, `token_count`, `scope`). Full content hydration occurs strictly on explicit user/agent demand.
- **Strict Optimistic Concurrency Control (OCC)**: Memory updates require providing `expected_revision`. Stale revisions trigger collision rejection with a 3-way unified diff and `*StaleRevisionError`.
- **Cryptographic Blame & Provenance**: All revision records are append-only. Every edit mandates author identification, agent model, machine ID, task ID, rationale, and a cryptographic SHA-256 hash linked to its parent hash.
- **Fenced Operational Concurrency**: Task leases use `FOR UPDATE SKIP LOCKED` and issue a unique monotonic `lease_id` fencing token. Submissions or renewals presenting a stale `lease_id` are rejected with `ErrLeaseLost`.
- **Poison-Pill Quarantine**: Tasks exceeding `max_retries = 3` fail automatically into `failed` status and record an unalterable autopsy handoff tombstone.
- **Transactional Outbox Event Sourcing**: Every state mutation (memory creation/update, task transition, handoff record) writes to `engram_events` in the same database transaction and fans out to real-time subscribers via `LISTEN engram_events`.

## Global Side Effects & Resource Footprint
- **Database Session Pool**: Managed `*pgxpool.Pool` with bounded max connections and idle cleanup.
- **Dedicated Outbox Listener**: 1 persistent TCP socket connection running `LISTEN engram_events` decoupled from the general connection pool.
- **SSE Stream Multiplexing**: Asynchronous in-memory fan-out channels (capacity 32) delivering events to active HTTP SSE consumers.

## Verification Command
```bash
go test -v -count=1 ./...
go build ./cmd/engram && go build ./cmd/engramd
```
