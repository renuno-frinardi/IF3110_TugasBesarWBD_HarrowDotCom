DROP TABLE IF EXISTS execution_batches;
DROP TABLE IF EXISTS executions;
DROP TABLE IF EXISTS approval_groups;
DROP TABLE IF EXISTS approval_requests;
DROP TABLE IF EXISTS migration_revisions;
ALTER TABLE migrations DROP COLUMN IF EXISTS superseded_at;
ALTER TABLE migrations DROP COLUMN IF EXISTS active;
ALTER TABLE user_groups DROP COLUMN IF EXISTS leader_user_id;