# db

## Overview
Manages PostgreSQL connection pooling, lexical migration execution, and asynchronous outbox event streaming via dedicated `LISTEN` connections and in-memory pub/sub fan-out for SSE consumers.

## Interface
- `Config` (`internal/platform/db/db.go:Config`): Connection parameters (`Host`, `Port`, `User`, `Password`, `Database`, `SSLMode`, `MaxConns`, `MinConns`) and DSN builder `DSN()`.
- `NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error)` (`internal/platform/db/db.go:NewPool`): Initializes `pgxpool.Pool` with connection limits, timeouts, and ping connectivity probe.
- `ApplyMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) error` (`internal/platform/db/migrate.go:ApplyMigrations`): Reads and applies sorted `.sql` migrations.
- `OutboxListener` (`internal/platform/db/listener.go:OutboxListener`): Asynchronous change broadcaster using a dedicated `pgx.Conn` subscribed to `LISTEN engram_events`.
  - `NewOutboxListener(pool *pgxpool.Pool, dsn string) *OutboxListener`: Constructor.
  - `Subscribe(projectID string) (<-chan []byte, func())`: Registers an in-memory client channel with buffer capacity 32 and returns teardown function.
  - `Start(ctx context.Context) error`: Establishes dedicated connection, executes `LISTEN engram_events`, and polls missed events via `GetEventsAfter`.

## Invariants
- Dedicated Listener Connection: `OutboxListener` allocates an isolated `pgx.Conn` instead of consuming a connection from `pgxpool.Pool`.
- Sequential Migrations: Migrations execute in lexical filename order.
- Non-blocking Broadcast: Subscriber channels drop overflowing messages when client buffer (32 items) is saturated.
- Outbox Reconciliation: Periodic notification timeouts poll unread events since `lastSeenID` to ensure no dropped events.

## Side Effects
- Allocates PostgreSQL session connections via `pgxpool.Pool`.
- Starts 1 persistent background goroutine and 1 dedicated TCP socket connection for `LISTEN engram_events`.
- Manages in-memory pub/sub channels synchronized via `sync.RWMutex`.

## Verification
- Run: `go test -v ./internal/platform/db/...`
