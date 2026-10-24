package migration

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DatabaseAccess struct {
	pool *pgxpool.Pool
}

func NewDatabaseAccess(pool *pgxpool.Pool) *DatabaseAccess {
	return &DatabaseAccess{
		pool: pool,
	}
}

func (d *DatabaseAccess) CanAccess(ctx context.Context, userID int, databaseID int, needEdit bool) (bool, error) {
	query := `SELECT EXISTS(
		SELECT 1
		FROM user_database_access access
		JOIN database_connections db ON db.id = access.database_connection_id
		WHERE access.user_id = $1
			AND access.database_connection_id = $2
			AND db.is_active
			AND (
				access.access_level = 'view_edit' OR (access.access_level = 'view_only' AND NOT $3)
				)
			)`

	var allowed bool
	err := d.pool.QueryRow(ctx, query, userID, databaseID, needEdit).Scan(&allowed)

	if err != nil {
		return false, fmt.Errorf("gagal memeriksa akses database: %w", err)
	}

	return allowed, nil
}
