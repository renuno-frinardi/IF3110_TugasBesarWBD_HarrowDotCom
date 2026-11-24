ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS leader_user_id BIGINT REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE migrations
    ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS superseded_at TIMESTAMPTZ;

ALTER TABLE migrations DROP CONSTRAINT IF EXISTS status_valid;
ALTER TABLE migrations ADD CONSTRAINT status_valid CHECK (status IN ('draft', 'pending_review', 'approved', 'running', 'executed', 'failed', 'superseded'));

CREATE TABLE IF NOT EXISTS migration_revisions (
    id BIGSERIAL PRIMARY KEY,
    migration_id INT NOT NULL REFERENCES migrations (id) ON DELETE CASCADE,
    revision_no INT NOT NULL,
    up_sql TEXT NOT NULL,
    down_sql TEXT,
    irreversible BOOLEAN NOT NULL DEFAULT FALSE,
    irreversible_reason TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'single',
    batch_config JSONB NOT NULL DEFAULT '{}'::JSONB,
    status TEXT NOT NULL DEFAULT 'draft',
    content_hash TEXT NOT NULL,
    created_by BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approved_at TIMESTAMPTZ,
    superseded_at TIMESTAMPTZ,
    UNIQUE (migration_id, revision_no)
);

CREATE TABLE IF NOT EXISTS approval_requests (
    id BIGSERIAL PRIMARY KEY,
    migration_id INT NOT NULL REFERENCES migrations (id) ON DELETE CASCADE,
    revision_id BIGINT NOT NULL REFERENCES migration_revisions (id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    target_execution_id BIGINT,
    requested_by BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ,
    CHECK (action IN ('up', 'down', 'forward')),
    CHECK (status IN ('pending', 'approved', 'rejected', 'revoked'))
);

CREATE TABLE IF NOT EXISTS approval_groups (
    id BIGSERIAL PRIMARY KEY,
    approval_request_id BIGINT NOT NULL REFERENCES approval_requests (id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES user_groups (id) ON DELETE CASCADE,
    group_name TEXT NOT NULL,
    leader_user_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    approved_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    approved_at TIMESTAMPTZ,
    UNIQUE (approval_request_id, group_id)
);

CREATE TABLE IF NOT EXISTS executions (
    id BIGSERIAL PRIMARY KEY,
    job_id TEXT NOT NULL UNIQUE,
    migration_id INT NOT NULL REFERENCES migrations (id) ON DELETE CASCADE,
    revision_id BIGINT NOT NULL REFERENCES migration_revisions (id) ON DELETE RESTRICT,
    database_id INT NOT NULL REFERENCES database_connections (id) ON DELETE RESTRICT,
    action TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'single',
    batch_config JSONB NOT NULL DEFAULT '{}'::JSONB,
    approval_request_id BIGINT REFERENCES approval_requests (id) ON DELETE SET NULL,
    rollback_of BIGINT REFERENCES executions (id) ON DELETE SET NULL,
    sql_executed TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    rows_committed BIGINT NOT NULL DEFAULT 0,
    error_message TEXT,
    requested_by BIGINT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (action IN ('up', 'down', 'forward')),
    CHECK (status IN ('pending', 'running', 'success', 'failed', 'partial', 'partially_reverted'))
);

CREATE TABLE IF NOT EXISTS execution_batches (
    id BIGSERIAL PRIMARY KEY,
    execution_id BIGINT NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    batch_no INT NOT NULL,
    cursor_from TEXT NOT NULL DEFAULT '',
    cursor_to TEXT NOT NULL DEFAULT '',
    rows_affected BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'committed',
    committed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reverted_at TIMESTAMPTZ,
    UNIQUE (execution_id, batch_no)
);

INSERT INTO migration_revisions (
    migration_id, revision_no, up_sql, irreversible, irreversible_reason,
    status, content_hash, created_by, created_at
)
SELECT m.id, 1, m.sql, TRUE, 'Legacy migration imported from Milestone 1',
       CASE WHEN m.status = 'draft' THEN 'draft' ELSE 'approved' END,
       MD5(m.sql), m.created_by, m.created_at
FROM migrations m
WHERE NOT EXISTS (
    SELECT 1 FROM migration_revisions r WHERE r.migration_id = m.id
);

UPDATE migrations SET active = (status = 'executed');