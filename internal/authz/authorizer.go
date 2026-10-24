package authz

// Import pkg
import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Definisi error msg
var (
	ErrUnauthenticated = errors.New("unauthenticated: valid session required")
	ErrForbidden       = errors.New("forbidden: insufficient permissions")
)

// Mendefinisikan suatu aktor dalma menajalankan suatu aksi
type Principal struct {
	UserID   int64
	Username string
	IsAdmin  bool
}

// Identidfikasi resource dari suatu target
type Resource struct {
	DatabaseID int64
	Source     string
}

// Otorisasi akses kontrol BE
type Authorizer interface {
	Can(ctx context.Context, principal Principal, action Action, resource Resource) error
}

// Evalueasi langsung permission dengan postgress
type DBAuthorizer struct {
	db *sql.DB
}

func NewDBAuthorizer(db *sql.DB) *DBAuthorizer {
	return &DBAuthorizer{db: db}
}

// Mengecek apakah sebuah principal diperbolehkan untuk mengakses suatu resource atau melakukan suatu aksi
func (a *DBAuthorizer) Can(ctx context.Context, principal Principal, action Action, resource Resource) error {
	if principal.UserID <= 0 {
		return ErrUnauthenticated
	}

	var isActive, isAdmin bool
	err := a.db.QueryRowContext(ctx, `
		SELECT u.is_active,
		       EXISTS (
		           SELECT 1 FROM user_roles ur
		           JOIN roles r ON r.id = ur.role_id
		           WHERE ur.user_id = u.id AND LOWER(r.name) = 'admin'
		       ) AS is_admin
		FROM users u
		WHERE u.id = $1
	`, principal.UserID).Scan(&isActive, &isAdmin)
	if errors.Is(err, sql.ErrNoRows) || !isActive {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("verify principal status: %w", err)
	}

	switch action {
	case ActionPlatformAdmin:
		if !isAdmin {
			return ErrForbidden
		}
		return nil

	case ActionDBView, ActionMigrationCreate, ActionMigrationExecute:
		if resource.DatabaseID <= 0 {
			return ErrForbidden
		}
		level, err := a.EffectiveDatabaseAccess(ctx, principal.UserID, resource.DatabaseID)
		if err != nil {
			return err
		}
		if !AllowsDatabaseAction(level, action) {
			return ErrForbidden
		}
		return nil

	default:
		return ErrForbidden
	}
}

// Mengecek seluruh grup untuk user id x dalam mengakses database y dan menghitung level akses efektifnya
func (a *DBAuthorizer) EffectiveDatabaseAccess(ctx context.Context, userID, databaseID int64) (AccessLevel, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT gda.access_level
		FROM group_database_access gda
		JOIN user_group_members ugm ON ugm.group_id = gda.group_id
		JOIN user_groups ug ON ug.id = ugm.group_id
		JOIN users u ON u.id = ugm.user_id
		WHERE ugm.user_id = $1
		  AND gda.database_connection_id = $2
		  AND u.is_active = TRUE
	`, userID, databaseID)
	if err != nil {
		return LevelNone, fmt.Errorf("query database grants: %w", err)
	}
	defer rows.Close()

	var grants []AccessLevel
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return LevelNone, fmt.Errorf("scan grant level: %w", err)
		}
		if lvl, ok := ParseAccessLevel(raw); ok {
			grants = append(grants, lvl)
		}
	}
	if err := rows.Err(); err != nil {
		return LevelNone, err
	}

	return EffectiveAccessLevel(grants), nil
}

// Mengembalikan databse yang dapat diakses
func (a *DBAuthorizer) ListAccessibleDatabases(ctx context.Context, userID int64) (map[int64]AccessLevel, error) {
	result := make(map[int64]AccessLevel)
	if userID <= 0 {
		return result, nil
	}

	rows, err := a.db.QueryContext(ctx, `
		SELECT gda.database_connection_id, gda.access_level
		FROM group_database_access gda
		JOIN user_group_members ugm ON ugm.group_id = gda.group_id
		JOIN users u ON u.id = ugm.user_id
		WHERE ugm.user_id = $1
		  AND u.is_active = TRUE
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list accessible databases: %w", err)
	}
	defer rows.Close()

	grantsByDB := make(map[int64][]AccessLevel)
	for rows.Next() {
		var dbID int64
		var raw string
		if err := rows.Scan(&dbID, &raw); err != nil {
			return nil, fmt.Errorf("scan accessible database: %w", err)
		}
		if lvl, ok := ParseAccessLevel(raw); ok {
			grantsByDB[dbID] = append(grantsByDB[dbID], lvl)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for dbID, grants := range grantsByDB {
		if eff := EffectiveAccessLevel(grants); eff != LevelNone {
			result[dbID] = eff
		}
	}
	return result, nil
}
