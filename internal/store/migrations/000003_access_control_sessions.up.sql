DROP TABLE IF EXISTS sessions;

CREATE TABLE sessions (
    id BIGSERIAL PRIMARY KEY,
    id_hash BYTEA NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rotated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    prev_id_hash BYTEA,
    prev_valid_until TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    csrf_secret BYTEA NOT NULL,
    CONSTRAINT sessions_id_hash_not_empty CHECK (OCTET_LENGTH(id_hash) > 0),
    CONSTRAINT sessions_csrf_secret_not_empty CHECK (OCTET_LENGTH(csrf_secret) > 0)
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_prev_id_hash_idx ON sessions (prev_id_hash) WHERE prev_id_hash IS NOT NULL;
