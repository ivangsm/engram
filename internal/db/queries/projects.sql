-- name: CreateProject :one
INSERT INTO projects (id, name, description, settings)
VALUES ($1, $2, $3, COALESCE(sqlc.arg(settings)::jsonb, '{}'::jsonb))
ON CONFLICT (id) DO UPDATE
SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE projects.name END,
    description = CASE WHEN EXCLUDED.description <> '' THEN EXCLUDED.description ELSE projects.description END,
    settings = CASE 
        WHEN EXCLUDED.settings IS NOT NULL AND EXCLUDED.settings <> '{}'::jsonb THEN EXCLUDED.settings 
        ELSE projects.settings 
    END,
    deleted_at = NULL,
    updated_at = now()
RETURNING *;

-- name: GetProject :one
SELECT * FROM projects
WHERE id = $1 AND deleted_at IS NULL
LIMIT 1;

-- name: ListProjects :many
SELECT * FROM projects
WHERE deleted_at IS NULL
ORDER BY created_at DESC;

-- name: UpdateProjectSettings :one
UPDATE projects
SET name = $2,
    description = $3,
    settings = $4,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteProject :exec
UPDATE projects
SET deleted_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: PurgeProject :exec
DELETE FROM projects
WHERE id = $1;
