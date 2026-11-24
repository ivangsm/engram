-- name: CreateTask :one
INSERT INTO agent_tasks (
    project_id,
    id,
    title,
    description,
    status,
    priority,
    metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (project_id, id) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    priority = EXCLUDED.priority,
    metadata = EXCLUDED.metadata,
    updated_at = now()
RETURNING *;

-- name: GetTask :one
SELECT * FROM agent_tasks
WHERE project_id = $1 AND id = $2
LIMIT 1;

-- name: ListTasks :many
SELECT * FROM agent_tasks
WHERE project_id = $1
  AND ($2 = '' OR status = $2)
ORDER BY priority DESC, created_at ASC
LIMIT $3;

-- name: ClaimNextPendingTask :one
WITH target AS (
    SELECT sub.project_id, sub.id, sub.claim_count, sub.max_retries
    FROM agent_tasks sub
    WHERE sub.project_id = $1
      AND (sub.status = 'pending' OR (sub.status = 'in_progress' AND sub.lease_expires_at < now()))
    ORDER BY sub.priority DESC, sub.created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE agent_tasks t
SET status = CASE WHEN target.claim_count >= target.max_retries THEN 'failed' ELSE 'in_progress' END,
    leased_by = CASE WHEN target.claim_count >= target.max_retries THEN NULL ELSE $2 END,
    lease_id = CASE WHEN target.claim_count >= target.max_retries THEN NULL ELSE $3 END,
    claimed_at = now(),
    lease_expires_at = CASE WHEN target.claim_count >= target.max_retries THEN NULL ELSE now() + ($4 * interval '1 second') END,
    claim_count = t.claim_count + 1,
    updated_at = now()
FROM target
WHERE t.project_id = target.project_id AND t.id = target.id
RETURNING t.*;

-- name: ClaimSpecificTask :one
UPDATE agent_tasks
SET status = 'in_progress',
    leased_by = $3,
    lease_id = $4,
    claimed_at = now(),
    lease_expires_at = now() + ($5 * interval '1 second'),
    claim_count = claim_count + 1,
    updated_at = now()
WHERE agent_tasks.project_id = $1 
  AND agent_tasks.id = $2
  AND (agent_tasks.status = 'pending' OR (agent_tasks.status = 'in_progress' AND agent_tasks.lease_expires_at < now()))
RETURNING *;

-- name: RenewTaskLease :one
UPDATE agent_tasks
SET lease_expires_at = now() + ($4 * interval '1 second'),
    updated_at = now()
WHERE project_id = $1
  AND id = $2
  AND lease_id = $3
  AND status = 'in_progress'
RETURNING *;

-- name: CompleteTask :one
UPDATE agent_tasks
SET status = 'completed',
    completed_at = now(),
    leased_by = NULL,
    lease_id = NULL,
    lease_expires_at = NULL,
    updated_at = now()
WHERE project_id = $1
  AND id = $2
  AND (lease_id = $3 OR $3 IS NULL)
  AND status = 'in_progress'
RETURNING *;

-- name: FailTask :one
UPDATE agent_tasks
SET status = 'failed',
    completed_at = now(),
    leased_by = NULL,
    lease_id = NULL,
    lease_expires_at = NULL,
    updated_at = now()
WHERE project_id = $1
  AND id = $2
  AND (lease_id = $3 OR $3 IS NULL)
RETURNING *;

-- name: ReleaseTask :one
UPDATE agent_tasks
SET status = 'pending',
    leased_by = NULL,
    lease_id = NULL,
    lease_expires_at = NULL,
    updated_at = now()
WHERE project_id = $1
  AND id = $2
  AND (lease_id = $3 OR $3 IS NULL)
RETURNING *;
