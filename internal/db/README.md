# db

## Overview
Database access package containing type-safe Go structs, parameter models, and query execution methods compiled by `sqlc` from the PostgreSQL migrations and SQL query declarations.

## Interface
- `Queries` (`internal/db/db.go:Queries`): Main database query receiver struct.
  - `New(db DBTX) *Queries`: Constructs query instance bound to pool or connection.
  - `WithTx(tx pgx.Tx) *Queries`: Binds queries to an active PostgreSQL transaction.
- Models (`internal/db/models.go`):
  - `Project`: Project namespace entity.
  - `AgentMemory`: Reference memory entity with vectors and text ranking data.
  - `EngramRevision`: Immutable cryptographic revision ledger entry.
  - `AgentTask`: Operational task queue item with `LeaseID`, `ClaimCount`, and `MaxRetries`.
  - `AgentHandoff`: Immutable cross-agent handoff record.
  - `EngramEvent`: Transactional outbox event with monotonic `ID` and `Payload`.
- Subdirectories:
  - [queries](./queries/README.md): Annotated SQL source query files.

## Invariants
- Code in this directory (excluding `queries/`) is generated automatically by `sqlc` v1.31.1; manual edits to `*.sql.go` and `models.go` are strictly forbidden.
- Schema changes must be applied first to `migrations/` and query changes to `internal/db/queries/`, followed by executing `sqlc generate`.

## Side Effects
- Executes read and write operations against the PostgreSQL database using `pgx/v5`.

## Verification
- Run: `sqlc generate && go test ./...`
