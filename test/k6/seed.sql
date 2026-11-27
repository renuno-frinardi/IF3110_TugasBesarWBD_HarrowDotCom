\set ON_ERROR_STOP on
\set QUIET 1
\if :{?commit_seed}
\else
\set commit_seed false
\endif
\if :{?same_count}
\else
\set same_count 1300
\endif
\if :{?different_count}
\else
\set different_count 650
\endif
\if :{?first_db}
\else
\set first_db 3
\endif
\if :{?second_db}
\else
\set second_db 8
\endif
\pset format unaligned
\pset tuples_only on
\pset footer off
\o /tmp/k6-fixtures.json

BEGIN;

CREATE TEMP TABLE k6_connections (
    database_id INT PRIMARY KEY,
    is_primary BOOLEAN NOT NULL
);
INSERT INTO k6_connections VALUES (:'first_db', TRUE), (:'second_db', FALSE);

DO $$
BEGIN
    IF current_database() <> 'harrow_db' THEN
        RAISE EXCEPTION 'Refusing to seed database %, expected local harrow_db', current_database();
    END IF;

    IF (
        SELECT COUNT(*)
        FROM database_connections c
        JOIN k6_connections t ON t.database_id = c.id
        WHERE c.db_type = 'postgresql' AND c.is_active
    ) <> 2 THEN
        RAISE EXCEPTION 'Expected two active PostgreSQL target connections';
    END IF;
END
$$;

CREATE TEMP TABLE k6_seed_run AS
SELECT to_char(clock_timestamp(), 'YYYYMMDDHH24MISSUS') AS run_key;

CREATE TEMP TABLE k6_targets (
    fixture_case TEXT NOT NULL,
    database_id INT NOT NULL,
    ordinal INT NOT NULL,
    title TEXT NOT NULL UNIQUE
);

INSERT INTO k6_targets (fixture_case, database_id, ordinal, title)
SELECT 'same_database', target.database_id, n, format('k6-%s-same-%s', run_key, n)
FROM k6_seed_run
CROSS JOIN (SELECT database_id FROM k6_connections WHERE is_primary) AS target
CROSS JOIN generate_series(1, :'same_count'::INT) AS series(n);

INSERT INTO k6_targets (fixture_case, database_id, ordinal, title)
SELECT 'different_databases', target.database_id, n,
       format('k6-%s-diff-%s-%s', run_key, target.database_id, n)
FROM k6_seed_run
CROSS JOIN k6_connections AS target
CROSS JOIN generate_series(1, :'different_count'::INT) AS series(n);

CREATE TEMP TABLE k6_actor AS
SELECT MIN(u.id)::INT AS user_id
FROM users u
WHERE u.is_active
  AND (
      SELECT COUNT(*)
      FROM user_database_access a
      JOIN k6_connections t ON t.database_id = a.database_connection_id
      WHERE a.user_id = u.id AND a.access_level = 'view_edit'
  ) = 2;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM k6_actor WHERE user_id IS NOT NULL) THEN
        RAISE EXCEPTION 'No active user has View + Edit access to both target connections';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM k6_targets WHERE fixture_case = 'same_database')
       OR NOT EXISTS (SELECT 1 FROM k6_targets WHERE fixture_case = 'different_databases') THEN
        RAISE EXCEPTION 'Seed counts must both be greater than zero';
    END IF;
END
$$;

CREATE TEMP TABLE k6_seeded (
    fixture_case TEXT NOT NULL,
    ordinal INT NOT NULL,
    migration_id INT NOT NULL,
    database_id INT NOT NULL,
    actor_id INT NOT NULL
);

WITH inserted AS (
    INSERT INTO migrations (database_id, title, sql, status, created_by)
    SELECT t.database_id, t.title, 'SELECT 1;', 'approved', a.user_id
    FROM k6_targets t
    CROSS JOIN k6_actor a
    RETURNING id, database_id, title
)
INSERT INTO k6_seeded (fixture_case, ordinal, migration_id, database_id, actor_id)
SELECT t.fixture_case, t.ordinal, i.id, i.database_id, a.user_id
FROM inserted i
JOIN k6_targets t ON t.database_id = i.database_id AND t.title = i.title
CROSS JOIN k6_actor a;

INSERT INTO migration_revisions (
    migration_id, revision_no, up_sql, down_sql, irreversible,
    irreversible_reason, mode, batch_config, status, content_hash,
    created_by, approved_at
)
SELECT s.migration_id, 1, 'SELECT 1;', 'SELECT 1;', FALSE, '', 'single',
       '{"cursor_column":"","batch_size":0}'::JSONB, 'approved',
       'b451922b1a7a70ca2505f9b910d0922ca4f768976024a0198bd24ce7f3b931ce',
       s.actor_id, NOW()
FROM k6_seeded s;

INSERT INTO approval_requests (
    migration_id, revision_id, action, requested_by, status, decided_at
)
SELECT s.migration_id, r.id, 'up', s.actor_id, 'approved', NOW()
FROM k6_seeded s
JOIN migration_revisions r
  ON r.migration_id = s.migration_id
 AND r.revision_no = 1;

SELECT jsonb_pretty(jsonb_build_object(
    'same_database',
    COALESCE((
        SELECT jsonb_agg(
            jsonb_build_object('database_id', database_id, 'migration_id', migration_id)
            ORDER BY ordinal
        )
        FROM k6_seeded
        WHERE fixture_case = 'same_database'
    ), '[]'::JSONB),
    'different_databases',
    COALESCE((
        SELECT jsonb_agg(
            jsonb_build_object('database_id', database_id, 'migration_id', migration_id)
            ORDER BY ordinal, database_id
        )
        FROM k6_seeded
        WHERE fixture_case = 'different_databases'
    ), '[]'::JSONB)
));

\if :commit_seed
COMMIT;
\else
ROLLBACK;
\endif
\o
