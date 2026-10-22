CREATE TABLE IF NOT EXISTS database_connections (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    database_name TEXT NOT NULL,
    username TEXT NOT NULL,
    hash_password TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT database_connections_name_key UNIQUE (name)
);

CREATE INDEX IF NOT EXISTS database_connections_updated_at_idx ON database_connections (updated_at DESC);