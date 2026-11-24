# platform

## Overview
Foundational infrastructure primitives, database connectivity, error formatting, and authentication mechanisms consumed across core services, HTTP handlers, and MCP protocol layers.

## Subdirectories & Boundaries
- [apierr](./apierr/README.md): Standardized RFC 7807 Problem Details representation and HTTP error factory functions.
- [auth](./auth/README.md): Bearer token authentication middleware supporting HTTP headers, query params, and timing-safe checks.
- [db](./db/README.md): PostgreSQL connection pool initialization, migration execution, and transactional outbox event listeners.

## Invariants
- Zero Domain Logic: Infrastructure components must remain domain-agnostic and never import domain packages from `internal/core`.
- Standard RFC 7807: All HTTP errors exposed by platform adapters format as RFC 7807 problem details.
- Fail-Closed: Missing tokens or misconfigured authentication immediately reject unauthorized requests.

## Verification
- Run: `go test -v ./internal/platform/...`
