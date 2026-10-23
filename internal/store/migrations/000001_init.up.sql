CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    username TEXT NOT NULL,
    email TEXT,
    password_hash TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_username_not_blank CHECK (BTRIM(username) <> ''),
    CONSTRAINT users_email_not_blank CHECK (email IS NULL OR BTRIM(email) <> ''),
    CONSTRAINT users_password_hash_not_blank CHECK (BTRIM(password_hash) <> '')
);

CREATE UNIQUE INDEX users_username_key ON users (LOWER(username));
CREATE UNIQUE INDEX users_email_key ON users (LOWER(email)) WHERE email IS NOT NULL;

CREATE TABLE roles (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT roles_name_not_blank CHECK (BTRIM(name) <> '')
);

CREATE UNIQUE INDEX roles_name_key ON roles (LOWER(name));

CREATE TABLE permissions (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT permissions_name_not_blank CHECK (BTRIM(name) <> '')
);

CREATE UNIQUE INDEX permissions_name_key ON permissions (LOWER(name));

CREATE TABLE user_roles (
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    assigned_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX user_roles_role_id_idx ON user_roles (role_id);

CREATE TABLE role_permissions (
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission_id BIGINT NOT NULL REFERENCES permissions (id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE INDEX role_permissions_permission_id_idx ON role_permissions (permission_id);

INSERT INTO roles (name, description, is_system)
VALUES
    ('admin', 'Mengelola user, group, koneksi database, dan pemberian akses platform', TRUE),
    ('user', 'Pengguna platform tanpa hak administratif', TRUE)
ON CONFLICT DO NOTHING;

INSERT INTO permissions (name, description)
VALUES
    ('platform.users.manage', 'Membuat, mengubah, menonaktifkan, dan menghapus user platform'),
    ('platform.groups.manage', 'Membuat, mengubah, dan menghapus group, serta mengatur anggotanya'),
    ('platform.connections.manage', 'Membuat, mengubah, dan menghapus koneksi database target'),
    ('platform.access.manage', 'Memberikan dan mencabut akses database untuk group'),
    ('platform.audit.view', 'Melihat audit log aktivitas platform')
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT roles.id, permissions.id
FROM roles
CROSS JOIN permissions
WHERE roles.name = 'admin'
ON CONFLICT DO NOTHING;

CREATE TABLE database_connections (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    database_name TEXT NOT NULL,
    username TEXT NOT NULL,
    password_ciphertext BYTEA NOT NULL,
    db_type TEXT NOT NULL DEFAULT 'postgresql',
    ssl_mode TEXT NOT NULL DEFAULT 'prefer',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT database_connections_name_key UNIQUE (name),
    CONSTRAINT database_connections_name_not_blank CHECK (BTRIM(name) <> ''),
    CONSTRAINT database_connections_host_not_blank CHECK (BTRIM(host) <> ''),
    CONSTRAINT database_connections_database_name_not_blank CHECK (BTRIM(database_name) <> ''),
    CONSTRAINT database_connections_username_not_blank CHECK (BTRIM(username) <> ''),
    CONSTRAINT database_connections_db_type CHECK (db_type IN ('postgresql', 'mysql', 'mariadb')),
    CONSTRAINT database_connections_ssl_mode
        CHECK (ssl_mode IN ('disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full')),
    CONSTRAINT database_connections_password_ciphertext_not_empty
        CHECK (OCTET_LENGTH(password_ciphertext) > 0)
);

CREATE INDEX database_connections_updated_at_idx ON database_connections (updated_at DESC);

CREATE TABLE user_groups (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_groups_name_not_blank CHECK (BTRIM(name) <> '')
);

CREATE UNIQUE INDEX user_groups_name_key ON user_groups (LOWER(name));

CREATE TABLE user_group_members (
    group_id BIGINT NOT NULL REFERENCES user_groups (id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    added_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);

CREATE INDEX user_group_members_user_id_idx ON user_group_members (user_id);

CREATE TABLE group_database_access (
    group_id BIGINT NOT NULL REFERENCES user_groups (id) ON DELETE CASCADE,
    database_connection_id BIGINT NOT NULL REFERENCES database_connections (id) ON DELETE CASCADE,
    access_level TEXT NOT NULL,
    granted_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, database_connection_id),
    CONSTRAINT group_database_access_level_check
        CHECK (access_level IN ('view_only', 'view_edit'))
);

CREATE INDEX group_database_access_connection_id_idx
    ON group_database_access (database_connection_id);

CREATE VIEW user_database_access AS
SELECT
    memberships.user_id,
    db_access.database_connection_id,
    CASE
        WHEN BOOL_OR(db_access.access_level = 'view_edit') THEN 'view_edit'
        ELSE 'view_only'
    END AS access_level
FROM user_group_members AS memberships
JOIN user_groups AS db_groups ON db_groups.id = memberships.group_id
JOIN group_database_access AS db_access ON db_access.group_id = db_groups.id
JOIN users ON users.id = memberships.user_id
WHERE users.is_active
GROUP BY memberships.user_id, db_access.database_connection_id;

CREATE TABLE sessions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    csrf_token_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    idle_expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    rotated_at TIMESTAMPTZ,
    grace_expires_at TIMESTAMPTZ,
    replaced_by_session_id BIGINT REFERENCES sessions (id) ON DELETE SET NULL,
    revoked_at TIMESTAMPTZ,
    revoked_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    revocation_reason TEXT,
    ip_address INET,
    user_agent TEXT,
    CONSTRAINT sessions_token_hash_not_empty CHECK (OCTET_LENGTH(token_hash) > 0),
    CONSTRAINT sessions_csrf_token_hash_not_empty CHECK (OCTET_LENGTH(csrf_token_hash) > 0),
    CONSTRAINT sessions_idle_before_absolute CHECK (idle_expires_at <= absolute_expires_at),
    CONSTRAINT sessions_absolute_after_created CHECK (absolute_expires_at > created_at),
    CONSTRAINT sessions_grace_only_when_rotated
        CHECK (grace_expires_at IS NULL OR rotated_at IS NOT NULL),
    CONSTRAINT sessions_grace_after_rotation
        CHECK (grace_expires_at IS NULL OR grace_expires_at > rotated_at)
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_active_user_idx
    ON sessions (user_id, absolute_expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE audit_logs (
    id BIGSERIAL PRIMARY KEY,
    actor_user_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    database_connection_id BIGINT REFERENCES database_connections (id) ON DELETE SET NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT audit_logs_action_not_blank CHECK (BTRIM(action) <> ''),
    CONSTRAINT audit_logs_resource_type_not_blank CHECK (BTRIM(resource_type) <> ''),
    CONSTRAINT audit_logs_metadata_object CHECK (JSONB_TYPEOF(metadata) = 'object')
);

CREATE INDEX audit_logs_created_at_idx ON audit_logs (created_at DESC);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_user_id, created_at DESC);
CREATE INDEX audit_logs_connection_idx ON audit_logs (database_connection_id, created_at DESC);

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER database_connections_set_updated_at
    BEFORE UPDATE ON database_connections
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER user_groups_set_updated_at
    BEFORE UPDATE ON user_groups
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER group_database_access_set_updated_at
    BEFORE UPDATE ON group_database_access
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();