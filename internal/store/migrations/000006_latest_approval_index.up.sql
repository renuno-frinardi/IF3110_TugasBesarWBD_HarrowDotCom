CREATE INDEX approval_requests_migration_latest_idx
    ON approval_requests (migration_id, created_at DESC, id DESC)
    INCLUDE (action);
