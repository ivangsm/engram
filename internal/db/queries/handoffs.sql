-- name: CreateHandoff :one
INSERT INTO agent_handoffs (
    project_id,
    task_id,
    from_agent,
    to_agent,
    verdict,
    summary,
    reproducible_evidence,
    artifacts
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: ListHandoffsForTask :many
SELECT * FROM agent_handoffs
WHERE project_id = $1 AND task_id = $2
ORDER BY created_at DESC;

-- name: GetLatestHandoffForTask :one
SELECT * FROM agent_handoffs
WHERE project_id = $1 AND task_id = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: ListRecentHandoffs :many
SELECT * FROM agent_handoffs
WHERE project_id = $1
ORDER BY created_at DESC
LIMIT $2;
