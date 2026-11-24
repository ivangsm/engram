# engram CLI

## Overview
Command-line interface (CLI) companion for Engram providing direct developer access to memory manifest discovery, progressive hydration, hybrid search, cryptographic provenance blame, task leasing, and agent handoffs via JSON-RPC over HTTP.

## Interface
- `main` (`cmd/engram/main.go:main`): CLI entry point supporting subcommands:
  - `manifest <project_id> [--scope <scope>]`
  - `recall <project_id> <key>`
  - `search <project_id> <query> [--limit <n>]`
  - `store <project_id> <key> <content_or_file> --rationale <reason> [--scope <scope>] [--task <task_id>]`
  - `blame <project_id> <key>`
  - `claim <project_id> [task_id] [--leased-by <id>] [--ttl <seconds>]`
  - `handoff <project_id> <task_id> <verdict> <summary> <evidence>`
  - `version`

## Invariants
- `len(os.Args) >= 2` required; otherwise prints usage and terminates with exit code 1.
- Every subcommand enforces strict positional argument validation:
  - `manifest`: requires `<project_id>`.
  - `recall`: requires `<project_id>` and `<key>`.
  - `search`: requires `<project_id>` and `<query>`.
  - `store`: requires `<project_id>`, `<key>`, content, and non-empty `--rationale` flag.
  - `blame`: requires `<project_id>` and `<key>`.
  - `claim`: requires `<project_id>`.
  - `handoff`: requires `<project_id>`, `<task_id>`, `<verdict>`, `<summary>`, and `<evidence>`.
- Verdict argument in `handoff` is normalized to uppercase via `strings.ToUpper`.
- HTTP response status code must be 200 OK and JSON-RPC response error must be nil; otherwise formats error and terminates with exit code 1.

## Side Effects
- Issues HTTP POST requests to `ENGRAM_SERVER_URL + "/mcp/message"` with a 10-second timeout and Bearer token in `Authorization` header.
- Reads local filesystem files via `os.ReadFile` when `store` content resolves to an existing file path.
- Reads environment variables `ENGRAM_SERVER_URL`, `ENGRAM_AUTH_TOKEN`, and `USER`.
- Writes formatted output to `os.Stdout` and errors to `os.Stderr`.

## Verification
- Run: `go test ./...`
