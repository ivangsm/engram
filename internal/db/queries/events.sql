-- name: CreateEvent :one
INSERT INTO engram_events (
    project_id,
    topic,
    payload
) VALUES (
    $1, $2, $3
)
RETURNING *;

-- name: GetEventsAfter :many
SELECT * FROM engram_events
WHERE (@project_id::text = '' OR project_id = @project_id::text)
  AND id > @after_id
ORDER BY id ASC
LIMIT @max_limit;
