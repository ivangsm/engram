-- name: GetMemoryManifest :many
SELECT 
    id,
    project_id,
    key,
    scope,
    digest,
    token_count,
    latest_revision_id,
    updated_at
FROM agent_memories
WHERE project_id = $1
  AND ($2 = '' OR scope = $2)
  AND is_active = true
ORDER BY key ASC;

-- name: RecallMemory :one
SELECT 
    m.id,
    m.project_id,
    m.key,
    m.scope,
    m.digest,
    m.content,
    m.token_count,
    m.latest_revision_id,
    m.superseded_by,
    m.is_active,
    m.created_at,
    m.updated_at,
    COALESCE(r.content_hash, '')::varchar(64) AS content_hash,
    COALESCE(r.revision_number, 0)::int AS revision_number
FROM agent_memories m
LEFT JOIN engram_revisions r ON r.id = m.latest_revision_id
WHERE m.project_id = $1
  AND m.key = $2
  AND m.is_active = true
LIMIT 1;

-- name: UpsertMemory :one
INSERT INTO agent_memories (
    project_id,
    key,
    scope,
    digest,
    content,
    token_count,
    embedding
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (project_id, key) DO UPDATE
SET scope = EXCLUDED.scope,
    digest = EXCLUDED.digest,
    content = EXCLUDED.content,
    token_count = EXCLUDED.token_count,
    embedding = COALESCE(EXCLUDED.embedding, agent_memories.embedding),
    is_active = true,
    updated_at = now()
RETURNING *;

-- name: SetMemoryLatestRevision :exec
UPDATE agent_memories
SET latest_revision_id = $2,
    updated_at = now()
WHERE id = $1;

-- name: SupersedeMemory :exec
UPDATE agent_memories
SET superseded_by = $2,
    is_active = false,
    updated_at = now()
WHERE id = $1;

-- name: SearchMemoriesHybrid :many
SELECT 
    id,
    project_id,
    key,
    scope,
    digest,
    content,
    token_count,
    latest_revision_id,
    updated_at,
    COALESCE((1 - (embedding <=> $3))::float8, 0.0)::float8 AS vector_similarity,
    ts_rank(tsv, plainto_tsquery('english', $2))::float8 AS text_rank,
    (
        ($4::float8 * COALESCE((1 - (embedding <=> $3))::float8, 0.0)::float8) + 
        ((1 - $4::float8) * ts_rank(tsv, plainto_tsquery('english', $2))::float8)
    )::float8 AS combined_score
FROM agent_memories
WHERE project_id = $1
  AND is_active = true
  AND (
      tsv @@ plainto_tsquery('english', $2) 
      OR (embedding IS NOT NULL AND (embedding <=> $3) < $5::float8)
  )
ORDER BY combined_score DESC
LIMIT $6;

-- name: SearchMemoriesText :many
SELECT 
    id,
    project_id,
    key,
    scope,
    digest,
    content,
    token_count,
    latest_revision_id,
    updated_at,
    ts_rank(tsv, plainto_tsquery('english', $2))::float8 AS text_rank
FROM agent_memories
WHERE project_id = $1
  AND is_active = true
  AND tsv @@ plainto_tsquery('english', $2)
ORDER BY text_rank DESC
LIMIT $3;

-- name: PruneSupersededMemories :execrows
DELETE FROM agent_memories
WHERE project_id = $1
  AND is_active = false
  AND superseded_by IS NOT NULL;
