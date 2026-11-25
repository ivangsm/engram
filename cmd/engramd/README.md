# engramd

## Overview
Core daemon service binary for Engram hosting the Fiber HTTP web server, Huma v2 OpenAPI documentation, persistent PostgreSQL connection pool and outbox event listener, and authenticated Model Context Protocol (MCP) streamable HTTP and SSE transports.

## Interface
- `main` (`cmd/engramd/main.go:main`): Daemon entry point.
- HTTP Routes:
  - `GET /healthz`: Liveness and database connectivity health probe.
  - `GET /api/v1/health`: OpenAPI 3.1 health endpoint.
  - `GET /sse`, `GET /mcp/sse`: Server-Sent Events stream for MCP real-time notifications and outbox replay.
  - `POST /mcp/message`, `POST /mcp/rpc`: JSON-RPC 2.0 message handler for MCP tools and session management.
- Configuration Environment Variables:
  - `ENGRAM_PORT`: HTTP listen port (default: `8085`).
  - `ENGRAM_DB_HOST`: PostgreSQL database host (default: `localhost`).
  - `ENGRAM_DB_PORT`: PostgreSQL port (default: `5439`).
  - `ENGRAM_DB_USER`: Database user (default: `engram`).
  - `ENGRAM_DB_PASSWORD`: Database password (default: `engram_dev_secret`).
  - `ENGRAM_DB_NAME`: Database name (default: `engram`).
  - `ENGRAM_DB_SSLMODE`: SSL connection mode (default: `disable`).
  - `ENGRAM_AUTH_TOKEN`: Pre-shared Bearer token for MCP routes (default: `engram_dev_token_2026`).
  - `ENGRAM_MIGRATIONS_DIR`: Migration files directory (default: `migrations`).
  - `ENGRAM_EMBEDDING_PROVIDER`: Optional vector embedding provider (`none`, `openai`, `openai-compatible`, `gemini`).
  - `ENGRAM_EMBEDDING_API_KEY`: API key for configured embedding provider (fail-fast if required and missing).
  - `ENGRAM_EMBEDDING_MODEL`: Embedding model override (defaults: `text-embedding-3-small`, `gemini-embedding-001`).
  - `ENGRAM_EMBEDDING_BASE_URL`: Optional API base URL override.
  - `ENGRAM_EMBEDDING_DIMENSIONS`: Vector dimensions (default: `1536`).

## Invariants
- Operates in degraded mode if database connection fails at boot; `GET /healthz` returns HTTP 503 Service Unavailable until database recovery.
- SQL migrations in `ENGRAM_MIGRATIONS_DIR` execute automatically in lexicographical order upon boot if PostgreSQL connection pool is non-nil.
- Dedicated outbox listener worker connects via isolated `pgx.Conn` executing `LISTEN engram_events`, streaming outbox events to active SSE sessions.
- Graceful shutdown captures `SIGINT` and `SIGTERM` signals with a 5-second maximum drain timeout.

## Side Effects
- Opens and manages PostgreSQL socket pool (`pgxpool.Pool`) and dedicated background connection (`pgx.Conn`).
- Executes schema DDL migrations on PostgreSQL.
- Binds and listens on configured TCP port.
- Writes operational logs to standard output and error loggers.

## Verification
- Run: `go test ./...`
