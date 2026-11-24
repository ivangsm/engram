# state

## Overview
Core operational state plane managing concurrent agent task queues, atomic row-level lease claims (`FOR UPDATE SKIP LOCKED`), lease fencing tokens (`lease_id`), poison-pill starvation protection, and immutable cross-agent handoffs.

## Interface
- `Service` (`internal/core/state/service.go:Service`): Operational state and task coordinator.
  - `NewService(pool *pgxpool.Pool) *Service`: Constructor.
  - `CreateTask(ctx context.Context, projectID, id, title, description string, priority int32, metadata json.RawMessage) (*TaskDetail, error)`: Enqueues an operational task.
  - `GetTask(ctx context.Context, projectID, id string) (*TaskDetail, error)`: Retrieves task state by ID.
  - `ListTasks(ctx context.Context, projectID, status string, limit int32) ([]TaskDetail, error)`: Lists tasks by project and status.
  - `ClaimTask(ctx context.Context, projectID, taskID, leasedBy string, ttlSeconds int32) (*TaskDetail, error)`: Atomically acquires a task lease, generates a unique `lease_id` UUID, and handles poison-pill auto-failure.
  - `RenewTaskLease(ctx context.Context, projectID, taskID, leaseID string, extendSeconds int32) (*TaskDetail, error)`: Heartbeat lease renewal enforced by lease fencing token.
  - `CompleteTask(ctx context.Context, projectID, taskID, leaseID string) (*TaskDetail, error)`: Marks task completed and releases leases, validating fencing token.
  - `FailTask(ctx context.Context, projectID, taskID, leaseID, reason string) (*TaskDetail, error)`: Marks task as failed and logs failure autopsy tombstone.
  - `ReleaseTask(ctx context.Context, projectID, taskID, leaseID string) (*TaskDetail, error)`: Returns task to pending queue.
  - `RecordHandoff(ctx context.Context, in HandoffInput) (*HandoffDetail, error)`: Records an immutable cross-agent handoff adhering to standard contract.
  - `ListHandoffs(ctx context.Context, projectID, taskID string) ([]HandoffDetail, error)`: Retrieves handoff history for a task.
- `Handoff Validation` (`internal/core/state/handoff.go`):
  - `ValidateHandoff(in HandoffInput) error`: Validates summary word count, non-empty evidence, and verdict enum.
  - `CountWords(s string) int`: Whitespace-delimited word counter.
  - Errors: `ErrSummaryTooLong`, `ErrSummaryEmpty`, `ErrEvidenceEmpty`, `ErrInvalidVerdict`, `ErrTaskNotFoundOrBusy`, `ErrLeaseLost`.

## Invariants
- Handoff Contract: `summary` must contain $\le 150$ words, `reproducible_evidence` must not be empty, and `verdict` must be one of `PASS`, `FAIL`, `BLOCKED`, `NEEDS_REVIEW`, `HANDOFF`, or `LEASE_EXPIRED`.
- Lease Fencing: Mutations to in-progress tasks require valid `lease_id` UUID matching the active holder. If the lease expired and was claimed by another worker, the call fails immediately with `ErrLeaseLost`.
- Poison-Pill Guard: In `ClaimNextPendingTask`, if a task's `claim_count >= max_retries`, the query transitions the task to status `failed` and generates a diagnostic reaper tombstone in `agent_handoffs`.
- Outbox Notification: Every state transition writes an outbox record to `engram_events` and signals `NOTIFY engram_events, 'ping'`.

## Side Effects
- Modifies rows in `agent_tasks`, inserts rows into `agent_handoffs` and `engram_events`.
- Emits PostgreSQL `NOTIFY` pings on channel `engram_events`.

## Verification
- Run: `go test -v ./internal/core/state`
