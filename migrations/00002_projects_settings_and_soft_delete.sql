-- Engram Schema Migration 00002: Projects Settings and Soft Delete

ALTER TABLE projects ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ DEFAULT NULL;

CREATE INDEX IF NOT EXISTS idx_projects_active ON projects(id) WHERE deleted_at IS NULL;
