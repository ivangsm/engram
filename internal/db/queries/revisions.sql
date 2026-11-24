-- name: CreateRevision :one
INSERT INTO engram_revisions (
    memory_id,
    project_id,
    revision_number,
    author_user,
    author_agent_model,
    machine_id,
    task_id,
    rationale,
    content_hash,
    parent_hash,
    patch_diff,
    content,
    digest
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
RETURNING *;

-- name: GetLatestRevisionForMemory :one
SELECT * FROM engram_revisions
WHERE memory_id = $1
ORDER BY revision_number DESC
LIMIT 1;

-- name: ListRevisionsForMemory :many
SELECT 
    r.id,
    r.memory_id,
    r.project_id,
    r.revision_number,
    r.author_user,
    r.author_agent_model,
    r.machine_id,
    r.task_id,
    r.rationale,
    r.content_hash,
    r.parent_hash,
    r.patch_diff,
    r.digest,
    r.created_at
FROM engram_revisions r
WHERE r.memory_id = $1
ORDER BY r.revision_number DESC;

-- name: ListRevisionsForMemoryWithContent :many
SELECT * FROM engram_revisions
WHERE memory_id = $1
ORDER BY revision_number DESC;

-- name: GetRevisionByHash :one
SELECT * FROM engram_revisions
WHERE content_hash = $1
LIMIT 1;
