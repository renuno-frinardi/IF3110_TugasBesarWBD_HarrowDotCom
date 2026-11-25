DROP TABLE IF EXISTS benchmark_ledger;

CREATE TABLE benchmark_ledger (
    id BIGINT PRIMARY KEY,
    account_code TEXT NOT NULL,
    raw_payload TEXT NOT NULL,
    checksum TEXT NOT NULL DEFAULT '',
    migrated BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO benchmark_ledger (id, account_code, raw_payload, checksum, migrated)
SELECT
    g,
    'ACC-' || lpad((g % 10000)::text, 5, '0'),
    repeat(md5(g::text), 4),
    '',
    FALSE
FROM generate_series(1, 600000) AS g;

CREATE INDEX idx_benchmark_ledger_account_migrated
    ON benchmark_ledger (account_code, migrated);

ANALYZE benchmark_ledger;
