# queries

## Overview
SQL source files containing annotated PostgreSQL query declarations compiled by `sqlc` into type-safe Go data access methods.

## Interface
- `projects.sql`: Project namespace CRUD operations (`CreateProject`, `GetProject`, `DeleteProject`).
- `memories.sql`: Memory catalog manifests, progressive recall, upserts, revision linking, and dual-tier search (`GetMemoryManifest`, `RecallMemory`, `UpsertMemory`, `SetMemoryLatestRevision`, `SearchMemoriesHybrid`, `SearchMemoriesText`, `PruneSupersededMemories`).
- `revisions.sql`: Cryptographic revision ledger audit trail queries (`CreateRevision`, `GetLatestRevisionForMemory`, `ListRevisionsForMemory`, `ListRevisionsForMemoryWithContent`, `GetRevisionByHash`).
- `tasks.sql`: Task operational state plane queries including atomic CTE claim queries with poison-pill guards, heartbeat renewal, lease fencing validation, completion, failure, and release (`CreateTask`, `GetTask`, `ListTasks`, `ClaimNextPendingTask`, `ClaimSpecificTask`, `RenewTaskLease`, `CompleteTask`, `FailTask`, `ReleaseTask`).
- `handoffs.sql`: Cross-agent handoff log and diagnostic tombstone queries (`CreateHandoff`, `ListHandoffsForTask`, `GetLatestHandoffForTask`, `ListRecentHandoffs`).
- `events.sql`: Transactional outbox event insertion and replay queries (`CreateEvent`, `GetEventsAfter`).

## Invariants
- Queries adhere to `sqlc` v2 PostgreSQL annotations (`:one`, `:many`, `:exec`, `:execrows`).
- Concurrency control: task queue claims enforce row-level locking via `FOR UPDATE SKIP LOCKED`.
- Replay reliability: `GetEventsAfter` supports monotonic sequence traversal ordered by `id ASC`.

## Side Effects
- Mutates and queries PostgreSQL tables across `projects`, `agent_memories`, `engram_revisions`, `agent_tasks`, `agent_handoffs`, and `engram_events`.

## Verification
- Run: `sqlc generate && go test ./...`
