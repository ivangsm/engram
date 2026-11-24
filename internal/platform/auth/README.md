# auth

## Overview
Provides HTTP Bearer token authentication middleware for Fiber REST and MCP endpoints. Enforces constant-time token comparison and supports token resolution across HTTP headers and query strings for SSE/EventSource transports.

## Interface
- `Config` (`internal/platform/auth/auth.go:Config`): Configuration struct containing `BearerToken` and `HeaderName`.
- `New(token string) fiber.Handler` (`internal/platform/auth/auth.go:New`): Middleware factory enforcing authentication. Bypasses `/healthz`, `/livez`, `/readyz`. Fails closed if token is empty. Evaluates `Authorization: Bearer`, `?token=`, and `X-API-Key`.

## Invariants
- Health Bypass: Only `/healthz`, `/livez`, and `/readyz` paths bypass authentication.
- Fail-Closed: If `token` is empty string, all non-probe requests return HTTP 401 Unauthorized (`https://engram.dev/errors/unauthorized`).
- Token Extraction Order: 1) `Authorization: Bearer <token>`, 2) `?token=<token>`, 3) `X-API-Key: <token>`. Missing token returns HTTP 401.
- Constant-Time Comparison: Verifies token match via `crypto/subtle.ConstantTimeCompare` against timing attacks. Invalid token returns HTTP 403 Forbidden (`https://engram.dev/errors/forbidden`).

## Side Effects
- In-memory request header and query string parsing. Zero disk or network I/O.
- Returns RFC 7807 compliant problem details JSON on 401 and 403 responses.

## Verification
- Run: `go test -v ./internal/platform/auth/...`
