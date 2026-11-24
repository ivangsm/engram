# apierr

## Overview
Platform error package implementing RFC 7807 Problem Details structured errors integrated with Huma v2 OpenAPI responses.

## Interface
- `ProblemDetails` (`internal/platform/apierr/apierr.go:ProblemDetails`): Structured RFC 7807 error model.
  - `Error() string`: Returns error detail string.
  - `GetStatus() int`: Returns HTTP response status code for Huma v2.
- Factory Functions:
  - `NotFound(code, detail string) ProblemDetails`: Generates HTTP 404 response.
  - `Conflict(code, detail string) ProblemDetails`: Generates HTTP 409 response.
  - `BadRequest(code, detail string) ProblemDetails`: Generates HTTP 400 response.
  - `Internal(detail string) ProblemDetails`: Generates HTTP 500 response.

## Invariants
- Compile-Time Contract: `ProblemDetails` explicitly implements the `huma.StatusError` interface via `var _ huma.StatusError = (*ProblemDetails)(nil)`.
- Immutability: Factory constructors always assign standardized `Type` URIs, standard HTTP `Title` definitions, and corresponding status codes.

## Side Effects
- None (pure in-memory error representations).

## Verification
- Run: `go test ./...`
