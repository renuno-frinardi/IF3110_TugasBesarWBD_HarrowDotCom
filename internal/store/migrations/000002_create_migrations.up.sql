CREATE TABLE migrations ( 
    id SERIAL PRIMARY KEY,
    database_id INT NOT NULL,
    version INT,

    title TEXT NOT NULL,
    sql TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft',

    created_by INT NOT NULL,
    executed_by INT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP,
    start_time TIMESTAMP,
    finish_time TIMESTAMP,

    error_message TEXT,
    FOREIGN KEY(database_id) REFERENCES database_connections(id) ON DELETE RESTRICT,

    CONSTRAINT version_pos CHECK (version > 0),
    CONSTRAINT migration_db_unique UNIQUE (database_id, version),
    CONSTRAINT status_valid CHECK (status IN ('running', 'executed', 'failed', 'draft'))
);