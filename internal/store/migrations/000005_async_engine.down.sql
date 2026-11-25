DROP TABLE IF EXISTS database_migration_locks;
DROP TABLE IF EXISTS migration_jobs;
DROP TABLE IF EXISTS execution_attempts;

ALTER TABLE executions
    DROP COLUMN IF EXISTS attempt;
