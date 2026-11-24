# memories

## Overview
Core domain package managing semantic memories, progressive disclosure manifests, cryptographic blame provenance, unified diff generation, and strict Optimistic Concurrency Control (OCC).

## Interface
- `Service` (`internal/core/memories/service.go:Service`): Memory management service backed by PostgreSQL pool.
  - `NewService(pool *pgxpool.Pool) *Service`: Constructor.
  - `GetManifest(ctx context.Context, projectID, scope string) ([]ManifestItem, error)`: Skeletal manifest query (<1,500 tokens across 100 entries).
  - `Recall(ctx context.Context, projectID, key string) (*MemoryDetail, error)`: Full content hydration for a specific memory key.
  - `Store(ctx context.Context, in StoreInput) (*StoreResult, error)`: Stores memory, enforces OCC validation, appends cryptographic revision, writes outbox event.
  - `Blame(ctx context.Context, projectID, key string) (*BlameResult, error)`: Returns full revision audit trail, authors, diffs, and cryptographic hash chain verification.
  - `Search(ctx context.Context, projectID, query string, embedding []float32, limit int32) ([]SearchItem, error)`: Dual-tier hybrid search (vector cosine similarity combined with fulltext rank).
  - `EstimateTokens(text string) int32`: Calibrated BPE token estimator (~3.7 characters per token).
- `Blame Engine` (`internal/core/memories/blame.go`):
  - `ComputeSHA256(content string) string`: Computes SHA-256 hash with strict CRLF to LF line ending normalization.
  - `GenerateUnifiedDiff(oldContent, newContent, label string) (string, error)`: Produces contextual unified diffs via `difflib`.
  - `VerifyHashChain(chain []RevisionChainItem) (bool, string)`: Validates parent hash pointers and content integrity across revisions.
- `StaleRevisionError` (`internal/core/memories/service.go:StaleRevisionError`): Structured error returned upon OCC hash collisions containing current HEAD content and unified diff.

## Invariants
- Strict Optimistic Concurrency Control: If `StoreInput.ExpectedParentHash` is supplied and does not match the current revision hash, the write is aborted and returns `*StaleRevisionError`.
- All SHA-256 content hashes normalize CRLF (`\r\n`) to LF (`\n`) before hashing.
- Unbroken hash chain: Revision 1 must have an empty/nil parent hash; every revision $N > 1$ must satisfy `parent_hash == revision[N-1].content_hash`.
- Transactional atomicity: `UpsertMemory`, `CreateRevision`, `SetMemoryLatestRevision`, and outbox `CreateEvent` execute within a single transaction `pgx.Tx`.

## Side Effects
- Mutates `projects`, `agent_memories`, `engram_revisions`, and `engram_events` in PostgreSQL.
- Executes `NOTIFY engram_events, 'ping'` over PostgreSQL socket upon successful commit.

## Verification
- Run: `go test -v ./internal/core/memories`
