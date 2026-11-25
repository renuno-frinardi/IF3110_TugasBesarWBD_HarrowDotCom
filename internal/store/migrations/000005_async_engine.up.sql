ALTER TABLE executions
    ADD COLUMN IF NOT EXISTS attempt INT NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS execution_attempts (
    id BIGSERIAL PRIMARY KEY,
    execution_id BIGINT NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    job_id TEXT NOT NULL,
    attempt_no INT NOT NULL,
    job_kind TEXT NOT NULL DEFAULT 'execute',
    status TEXT NOT NULL,
    rows_committed BIGINT NOT NULL DEFAULT 0,
    error_message TEXT,
    sql_executed TEXT NOT NULL,
    worker_id TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (execution_id, attempt_no),
    CHECK (status IN ('running', 'success', 'failed', 'partial', 'partially_reverted'))
);

CREATE INDEX IF NOT EXISTS idx_execution_attempts_execution
    ON execution_attempts (execution_id, attempt_no DESC);

CREATE TABLE IF NOT EXISTS migration_jobs (
    id BIGSERIAL PRIMARY KEY,
    job_id TEXT NOT NULL,
    execution_id BIGINT NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    database_id INT NOT NULL REFERENCES database_connections (id) ON DELETE CASCADE,
    migration_id INT NOT NULL REFERENCES migrations (id) ON DELETE CASCADE,
    revision_id BIGINT NOT NULL REFERENCES migration_revisions (id) ON DELETE RESTRICT,
    job_kind TEXT NOT NULL DEFAULT 'execute',
    action TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'normal',
    batch_config JSONB NOT NULL DEFAULT '{}'::JSONB,
    sql_text TEXT NOT NULL,
    down_sql_text TEXT NOT NULL DEFAULT '',
    irreversible BOOLEAN NOT NULL DEFAULT FALSE,
    requested_by BIGINT,
    trace_context TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    attempt INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_by TEXT,
    locked_at TIMESTAMPTZ,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (job_kind IN ('execute', 'resume', 'cancel_batches')),
    CHECK (action IN ('up', 'down', 'forward')),
    CHECK (status IN ('queued', 'processing', 'completed', 'dead', 'cancelled'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_migration_jobs_active_kind
    ON migration_jobs (job_id, job_kind)
    WHERE status IN ('queued', 'processing');

CREATE INDEX IF NOT EXISTS idx_migration_jobs_poll
    ON migration_jobs (status, next_run_at, id);

CREATE INDEX IF NOT EXISTS idx_migration_jobs_job_id
    ON migration_jobs (job_id, id DESC);

CREATE TABLE IF NOT EXISTS database_migration_locks (
    database_id INT PRIMARY KEY REFERENCES database_connections (id) ON DELETE CASCADE,
    holder_worker_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    execution_id BIGINT NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    fencing_token BIGINT NOT NULL DEFAULT 1,
    state TEXT NOT NULL DEFAULT 'active',
    reason TEXT,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_until TIMESTAMPTZ NOT NULL,
    CHECK (state IN ('active', 'blocked_partial', 'blocked_uncertain'))
);
