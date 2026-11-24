# migrations

## Overview
SQL migration scripts defining the canonical PostgreSQL 18 database schema, extensions, indexes, and constraints for Engram.

## Interface
- `00001_initial_schema.sql`: Initial canonical schema initializing:
  - Extensions: `uuid-ossp`, `pgcrypto`, `pg_trgm`, `vector`.
  - Tables:
    - `projects`: Project context isolation namespaces.
    - `agent_memories`: Semantic memories with progressive disclosure digests, `vector(1536)` embeddings, and `tsvector` fulltext search.
    - `engram_revisions`: Append-only cryptographic blame ledger capturing author, agent model, machine, task, rationale, content snapshot, unified diffs, and SHA-256 parent hash chains.
    - `agent_tasks`: Operational task queue supporting atomic row-level lease claims (`SKIP LOCKED`), lease fencing (`lease_id`), and poison-pill guards (`max_retries`).
    - `agent_handoffs`: Immutable structured handoff audit trails with strict word limits, verdict validation, and reproducible evidence.
    - `engram_events`: Durable transactional outbox table (`id BIGSERIAL PRIMARY KEY`, monotonic) powering reliable SSE event replay.

## Invariants
- All DDL definitions use `CREATE ... IF NOT EXISTS` to ensure idempotent execution.
- Relational integrity: child tables enforce strict foreign key constraints referencing `projects(id)` with `ON DELETE CASCADE`.
- `engram_revisions` maintains an immutable append-only constraint with unique key on `(memory_id, revision_number)`.
- `agent_tasks.status` is strictly constrained to `('pending', 'in_progress', 'completed', 'failed', 'blocked')`.
- `agent_handoffs.verdict` is strictly constrained to `('PASS', 'FAIL', 'BLOCKED', 'NEEDS_REVIEW', 'HANDOFF', 'LEASE_EXPIRED')`.
- Sequential migration runner executes SQL files in lexicographical filename order.

## Side Effects
- Mutates database catalog, provisions PostgreSQL extensions, creates tables, GIN indexes, and HNSW vector cosine indexes.

## Verification
- Run: `go test ./...`
