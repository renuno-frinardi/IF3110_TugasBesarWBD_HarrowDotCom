package migration

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	impl "github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/store"
)

type Migration = impl.Migration
type Service = impl.Service
type Store = impl.Store
type DatabaseAccess = impl.DatabaseAccess

const (
	S_Draft    = impl.StatusDraft
	S_Running  = impl.StatusRunning
	S_Executed = impl.StatusExecuted
	S_Failed   = impl.StatusFailed
)

var (
	InvalidInput     = impl.InvalidInput
	Forbidden        = impl.Forbidden
	NotDraft         = impl.NotDraft
	ExecutionPending = impl.ExecutionPending
)

func NewStore(pool *pgxpool.Pool) *Store {
	return impl.NewStore(pool)
}

func NewDatabaseAccess(pool *pgxpool.Pool) *DatabaseAccess {
	return impl.NewDatabaseAccess(pool)
}

func NewService(store *Store, access *DatabaseAccess) *Service {
	return impl.NewService(store, access)
}

func validateSQL(sqlText string) error {
	return impl.ValidateSQL(sqlText)
}

func targetConfig(connection store.DatabaseConnection, password string) (*pgx.ConnConfig, error) {
	return impl.TargetConfig(connection, password)
}

func runMariaDBMigrationSQL(ctx context.Context, target *sql.DB, sqlText string) (string, error) {
	return impl.RunMariaDBMigrationSQL(ctx, target, sqlText)
}

func rollbackExecution(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
