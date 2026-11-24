-- Engram Canonical Database Schema for PostgreSQL 18 + pgvector + pg_trgm

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";
CREATE EXTENSION IF NOT EXISTS "vector";

-- 1. Projects (Isolated context namespaces)
CREATE TABLE IF NOT EXISTS projects (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. Agent Memories (Semantic memory, progressive disclosure digests, embeddings)
CREATE TABLE IF NOT EXISTS agent_memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    key VARCHAR(255) NOT NULL,
    scope VARCHAR(64) NOT NULL DEFAULT 'global',
    digest TEXT NOT NULL,
    content TEXT NOT NULL,
    token_count INT NOT NULL DEFAULT 0,
    embedding vector(1536),
    tsv tsvector GENERATED ALWAYS AS (to_tsvector('english', key || ' ' || digest || ' ' || content)) STORED,
    latest_revision_id UUID,
    superseded_by UUID REFERENCES agent_memories(id) ON DELETE SET NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_agent_memories_project_key UNIQUE (project_id, key)
);

CREATE INDEX IF NOT EXISTS idx_agent_memories_scope ON agent_memories(project_id, scope) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_agent_memories_tsv ON agent_memories USING GIN(tsv);
CREATE INDEX IF NOT EXISTS idx_agent_memories_trgm ON agent_memories USING GIN(key gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_agent_memories_hnsw ON agent_memories USING hnsw (embedding vector_cosine_ops);

-- 3. Engram Revisions (Append-only strict cryptographic blame & provenance audit trail)
CREATE TABLE IF NOT EXISTS engram_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    memory_id UUID NOT NULL REFERENCES agent_memories(id) ON DELETE CASCADE,
    project_id VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    revision_number INT NOT NULL,
    author_user VARCHAR(128) NOT NULL,
    author_agent_model VARCHAR(128) NOT NULL,
    machine_id VARCHAR(128) NOT NULL,
    task_id VARCHAR(128) NOT NULL,
    rationale TEXT NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    parent_hash VARCHAR(64),
    patch_diff TEXT,
    content TEXT NOT NULL,
    digest TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_engram_revisions_memory_rev UNIQUE (memory_id, revision_number)
);

CREATE INDEX IF NOT EXISTS idx_revisions_memory_lookup ON engram_revisions(memory_id, revision_number DESC);
CREATE INDEX IF NOT EXISTS idx_revisions_content_hash ON engram_revisions(content_hash);
CREATE INDEX IF NOT EXISTS idx_revisions_task_id ON engram_revisions(project_id, task_id);

ALTER TABLE agent_memories 
    ADD CONSTRAINT fk_agent_memories_latest_revision 
    FOREIGN KEY (latest_revision_id) REFERENCES engram_revisions(id) ON DELETE SET NULL;

-- 4. Agent Tasks (Operational state plane & queue with SKIP LOCKED leases)
CREATE TABLE IF NOT EXISTS agent_tasks (
    project_id VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    id VARCHAR(128) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'in_progress', 'completed', 'failed', 'blocked')),
    priority INT NOT NULL DEFAULT 0,
    leased_by VARCHAR(128),
    lease_id UUID,
    lease_expires_at TIMESTAMPTZ,
    claimed_at TIMESTAMPTZ,
    claim_count INT NOT NULL DEFAULT 0,
    max_retries INT NOT NULL DEFAULT 3,
    completed_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, id)
);

CREATE INDEX IF NOT EXISTS idx_agent_tasks_pending ON agent_tasks(project_id, status, priority DESC, created_at ASC) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_agent_tasks_lease ON agent_tasks(project_id, lease_expires_at) WHERE status = 'in_progress';

-- 5. Agent Handoffs (Immutable cross-agent state transfers)
CREATE TABLE IF NOT EXISTS agent_handoffs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id VARCHAR(128) NOT NULL,
    from_agent VARCHAR(128) NOT NULL,
    to_agent VARCHAR(128),
    verdict VARCHAR(64) NOT NULL
        CHECK (verdict IN ('PASS', 'FAIL', 'BLOCKED', 'NEEDS_REVIEW', 'HANDOFF', 'LEASE_EXPIRED')),
    summary TEXT NOT NULL,
    reproducible_evidence TEXT NOT NULL,
    artifacts JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (project_id, task_id) REFERENCES agent_tasks(project_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_handoffs_task ON agent_handoffs(project_id, task_id, created_at DESC);

-- 6. Engram Events (Durable Transactional Outbox for SSE & cross-machine state replay)
CREATE TABLE IF NOT EXISTS engram_events (
    id BIGSERIAL PRIMARY KEY,
    project_id VARCHAR(64) NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    topic VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_engram_events_stream ON engram_events(project_id, id ASC);
