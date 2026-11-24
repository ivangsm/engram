# mcp

## Overview
Model Context Protocol (MCP) server package implementing the official Anthropic/Antigravity JSON-RPC 2.0 protocol over Streamable HTTP and Server-Sent Events (SSE).

## Interface
- `Server` (`internal/mcp/server.go:Server`): MCP protocol server.
  - `NewServer(registry *Registry) *Server`: Constructor.
  - `SetEventListener(l EventListener)`: Attaches outbox event listener for SSE fan-out.
  - `RegisterRoutes(app fiber.Router)`: Mounts `/sse`, `/mcp/sse`, `/mcp/message`, `/mcp/rpc`.
- `Registry` (`internal/mcp/tools.go:Registry`): MCP tool registry and dispatcher.
  - `NewRegistry(m *memories.Service, s *state.Service) *Registry`: Constructor.
  - `ListTools() []Tool`: Returns tool specifications and JSON schemas for:
    - `list_memory_manifest`: Progressive disclosure skeletal catalog.
    - `recall_memory`: On-demand memory hydration.
    - `search_memory`: Hybrid vector and text ranking.
    - `store_memory`: Immutable memory upsert with strict OCC.
    - `blame_memory`: Provenance, diffs, and hash chain verification.
    - `create_task`: Task enqueuing.
    - `claim_task`: Atomic lease acquisition via `SKIP LOCKED`.
    - `renew_task_lease`: Fenced heartbeat renewal.
    - `record_handoff`: Structured cross-agent handoff.
    - `complete_task`: Fenced task completion.
    - `fail_task`: Diagnostic task failure.
  - `ExecuteTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error)`: Tool invocation dispatcher.
- Protocol Types (`internal/mcp/types.go`): `JSONRPCRequest`, `JSONRPCResponse`, `RPCError`, `Tool`, `ToolSchema`.

## Invariants
- Protocol Compliance: Supports JSON-RPC 2.0 methods `initialize`, `notifications/initialized`, `ping`, `tools/list`, and `tools/call`.
- SSE Lifecycle: Client connections receive an initial `event: endpoint` payload pointing to `/mcp/message?sessionId={sessionID}`.
- Error Handling: Malformed JSON returns HTTP 400 with code -32700 (`ParseError`); unknown methods return code -32601 (`MethodNotFound`); OCC collisions return structured `ERR_STALE_REVISION` payloads with unified diffs.
- Clean Session Teardown: Disconnecting SSE clients causes immediate channel closure and cleanup from the active session map.

## Side Effects
- Streams HTTP chunked SSE events to connected agents.
- Forwards agent requests to core memory and task domain services.

## Verification
- Run: `go test -v ./internal/mcp`
