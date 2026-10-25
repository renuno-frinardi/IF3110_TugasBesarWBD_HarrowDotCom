package admin

// Import pkg
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/auth"
	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/authz"
)

// Definis eeror msg
var (
	ErrInvalidInput = errors.New("invalid input")
	ErrNotFound     = errors.New("record not found")
)

// Membentuk tipe user, group, database, audit, dan log
type UserRecord struct {
	ID        int64
	Username  string
	Email     string
	IsActive  bool
	IsAdmin   bool
	Groups    []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type GroupMember struct {
	UserID   int64
	Username string
	AddedAt  time.Time
}

type GroupRecord struct {
	ID          int64
	Name        string
	Description string
	Members     []GroupMember
	GrantCount  int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type DatabaseGrantRecord struct {
	GroupID              int64
	GroupName            string
	DatabaseConnectionID int64
	DatabaseName         string
	ConnectionName       string
	DBType               string
	AccessLevel          authz.AccessLevel
	GrantedAt            time.Time
	UpdatedAt            time.Time
}

type AuditLogRecord struct {
	ID                   int64
	ActorUserID          *int64
	ActorUsername        string
	Action               string
	ResourceType         string
	ResourceID           string
	DatabaseConnectionID *int64
	Metadata             string
	CreatedAt            time.Time
}

type Store struct {
	db       *sql.DB
	authSvc  *auth.Service
	sessions *auth.SessionManager
}

// Membentuk penyimpanan/modifikasi baru
func NewStore(db *sql.DB, authSvc *auth.Service, sessions *auth.SessionManager) *Store {
	return &Store{
		db:       db,
		authSvc:  authSvc,
		sessions: sessions,
	}
}

// List user
func (s *Store) ListUsers(ctx context.Context) ([]UserRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username, COALESCE(u.email, ''), u.is_active, u.created_at, u.updated_at,
		       EXISTS (
		           SELECT 1 FROM user_roles ur
		           JOIN roles r ON r.id = ur.role_id
		           WHERE ur.user_id = u.id AND LOWER(r.name) = 'admin'
		       ) AS is_admin
		FROM users u
		ORDER BY u.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []UserRecord
	userIdx := make(map[int64]int)
	for rows.Next() {
		var u UserRecord
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.IsAdmin); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		userIdx[u.ID] = len(users)
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	memRows, err := s.db.QueryContext(ctx, `
		SELECT ugm.user_id, ug.name
		FROM user_group_members ugm
		JOIN user_groups ug ON ug.id = ugm.group_id
		ORDER BY ug.name ASC
	`)
	if err == nil {
		defer memRows.Close()
		for memRows.Next() {
			var uid int64
			var gname string
			if err := memRows.Scan(&uid, &gname); err == nil {
				if idx, ok := userIdx[uid]; ok {
					users[idx].Groups = append(users[idx].Groups, gname)
				}
			}
		}
	}

	return users, nil
}

// Membentuk user
func (s *Store) CreateUser(ctx context.Context, actorID int64, username, email, password string, isAdmin bool) (*UserRecord, error) {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)
	if username == "" || len(username) > 50 || password == "" {
		return nil, fmt.Errorf("%w: username and password are required", ErrInvalidInput)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var emailNull sql.NullString
	if email != "" {
		emailNull = sql.NullString{String: email, Valid: true}
	}

	var u UserRecord
	u.Username = username
	u.Email = email
	u.IsActive = true
	u.IsAdmin = isAdmin

	err = tx.QueryRowContext(ctx, `
		INSERT INTO users (username, email, password_hash, is_active)
		VALUES ($1, $2, $3, TRUE)
		RETURNING id, created_at, updated_at
	`, username, emailNull, hash).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	roleName := "user"
	if isAdmin {
		roleName = "admin"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_roles (user_id, role_id, assigned_by)
		SELECT $1, r.id, NULLIF($2, 0)
		FROM roles r
		WHERE LOWER(r.name) = $3
		ON CONFLICT (user_id, role_id) DO NOTHING
	`, u.ID, actorID, roleName)
	if err != nil {
		return nil, fmt.Errorf("assign role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	_ = s.RecordAuditLog(ctx, &actorID, "user.create", "user", fmt.Sprintf("%d", u.ID), nil, map[string]any{
		"username": u.Username,
		"is_admin": isAdmin,
	})
	return &u, nil
}

// Membentuk aktive user
func (s *Store) SetUserActive(ctx context.Context, actorID, targetUserID int64, isActive bool) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET is_active = $1, updated_at = NOW() WHERE id = $2
	`, isActive, targetUserID)
	if err != nil {
		return fmt.Errorf("update user active status: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}

	if !isActive && s.sessions != nil {
		_ = s.sessions.RevokeUserSessions(ctx, targetUserID)
	}

	action := "user.activate"
	if !isActive {
		action = "user.deactivate"
	}
	_ = s.RecordAuditLog(ctx, &actorID, action, "user", fmt.Sprintf("%d", targetUserID), nil, map[string]any{
		"is_active": isActive,
	})
	return nil
}

// Mengganti password user
func (s *Store) ResetUserPassword(ctx context.Context, actorID, targetUserID int64, newPassword string) error {
	if strings.TrimSpace(newPassword) == "" {
		return fmt.Errorf("%w: password cannot be empty", ErrInvalidInput)
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2
	`, hash, targetUserID)
	if err != nil {
		return fmt.Errorf("reset user password: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}

	if s.sessions != nil {
		_ = s.sessions.RevokeUserSessions(ctx, targetUserID)
	}

	_ = s.RecordAuditLog(ctx, &actorID, "user.reset_password", "user", fmt.Sprintf("%d", targetUserID), nil, nil)
	return nil
}

// Mengatur apakah user seorang admin
func (s *Store) SetUserAdmin(ctx context.Context, actorID, targetUserID int64, isAdmin bool) error {
	if isAdmin {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO user_roles (user_id, role_id, assigned_by)
			SELECT $1, r.id, NULLIF($2, 0)
			FROM roles r
			WHERE LOWER(r.name) = 'admin'
			ON CONFLICT (user_id, role_id) DO NOTHING
		`, targetUserID, actorID)
		if err != nil {
			return fmt.Errorf("grant admin role: %w", err)
		}
	} else {
		_, err := s.db.ExecContext(ctx, `
			DELETE FROM user_roles
			WHERE user_id = $1
			  AND role_id IN (SELECT id FROM roles WHERE LOWER(name) = 'admin')
		`, targetUserID)
		if err != nil {
			return fmt.Errorf("revoke admin role: %w", err)
		}
		if s.sessions != nil {
			_ = s.sessions.RevokeUserSessions(ctx, targetUserID)
		}
	}

	_ = s.RecordAuditLog(ctx, &actorID, "user.set_admin", "user", fmt.Sprintf("%d", targetUserID), nil, map[string]any{
		"is_admin": isAdmin,
	})
	return nil
}

// Menghilangkan user
func (s *Store) DeleteUser(ctx context.Context, actorID, targetUserID int64) error {
	if s.sessions != nil {
		_ = s.sessions.RevokeUserSessions(ctx, targetUserID)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, targetUserID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	_ = s.RecordAuditLog(ctx, &actorID, "user.delete", "user", fmt.Sprintf("%d", targetUserID), nil, nil)
	return nil
}

// Melakukan listing group
func (s *Store) ListGroups(ctx context.Context) ([]GroupRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ug.id, ug.name, ug.description, ug.created_at, ug.updated_at,
		       (SELECT COUNT(*) FROM group_database_access gda WHERE gda.group_id = ug.id) AS grant_count
		FROM user_groups ug
		ORDER BY ug.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var groups []GroupRecord
	idxByID := make(map[int64]int)
	for rows.Next() {
		var g GroupRecord
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt, &g.GrantCount); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		idxByID[g.ID] = len(groups)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	memRows, err := s.db.QueryContext(ctx, `
		SELECT ugm.group_id, u.id, u.username, ugm.added_at
		FROM user_group_members ugm
		JOIN users u ON u.id = ugm.user_id
		ORDER BY u.username ASC
	`)
	if err == nil {
		defer memRows.Close()
		for memRows.Next() {
			var gid int64
			var m GroupMember
			if err := memRows.Scan(&gid, &m.UserID, &m.Username, &m.AddedAt); err == nil {
				if idx, ok := idxByID[gid]; ok {
					groups[idx].Members = append(groups[idx].Members, m)
				}
			}
		}
	}

	return groups, nil
}

// Membuat grup
func (s *Store) CreateGroup(ctx context.Context, actorID int64, name, description string) (*GroupRecord, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" || len(name) > 100 {
		return nil, fmt.Errorf("%w: group name must be between 1 and 100 characters", ErrInvalidInput)
	}

	var g GroupRecord
	g.Name = name
	g.Description = description
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO user_groups (name, description, created_by)
		VALUES ($1, $2, NULLIF($3, 0))
		RETURNING id, created_at, updated_at
	`, name, description, actorID).Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}

	_ = s.RecordAuditLog(ctx, &actorID, "group.create", "group", fmt.Sprintf("%d", g.ID), nil, map[string]any{
		"name": g.Name,
	})
	return &g, nil
}

// Melakukan update grup
func (s *Store) UpdateGroup(ctx context.Context, actorID, groupID int64, name, description string) error {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" || len(name) > 100 {
		return fmt.Errorf("%w: group name must be between 1 and 100 characters", ErrInvalidInput)
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE user_groups
		SET name = $1, description = $2, updated_at = NOW()
		WHERE id = $3
	`, name, description, groupID)
	if err != nil {
		return fmt.Errorf("update group: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}

	_ = s.RecordAuditLog(ctx, &actorID, "group.update", "group", fmt.Sprintf("%d", groupID), nil, map[string]any{
		"name": name,
	})
	return nil
}

// Melakukan deletion terhadap grup
func (s *Store) DeleteGroup(ctx context.Context, actorID, groupID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_groups WHERE id = $1`, groupID)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}

	_ = s.RecordAuditLog(ctx, &actorID, "group.delete", "group", fmt.Sprintf("%d", groupID), nil, nil)
	return nil
}

// Menambah member pada grup
func (s *Store) AddGroupMember(ctx context.Context, actorID, groupID, userID int64) error {
	if groupID <= 0 || userID <= 0 {
		return ErrInvalidInput
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO user_group_members (group_id, user_id, added_by)
		VALUES ($1, $2, NULLIF($3, 0))
		ON CONFLICT (group_id, user_id) DO NOTHING
	`, groupID, userID, actorID)
	if err != nil {
		return fmt.Errorf("add group member: %w", err)
	}

	_ = s.RecordAuditLog(ctx, &actorID, "group.member.add", "group", fmt.Sprintf("%d", groupID), nil, map[string]any{
		"user_id": userID,
	})
	return nil
}

// Menghilangkan member pada grup
func (s *Store) RemoveGroupMember(ctx context.Context, actorID, groupID, userID int64) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM user_group_members WHERE group_id = $1 AND user_id = $2
	`, groupID, userID)
	if err != nil {
		return fmt.Errorf("remove group member: %w", err)
	}

	_ = s.RecordAuditLog(ctx, &actorID, "group.member.remove", "group", fmt.Sprintf("%d", groupID), nil, map[string]any{
		"user_id": userID,
	})
	return nil
}

// List database pada grup
func (s *Store) ListDatabaseGrants(ctx context.Context) ([]DatabaseGrantRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT gda.group_id, ug.name, gda.database_connection_id,
		       dc.name, dc.database_name, dc.db_type,
		       gda.access_level, gda.granted_at, gda.updated_at
		FROM group_database_access gda
		JOIN user_groups ug ON ug.id = gda.group_id
		JOIN database_connections dc ON dc.id = gda.database_connection_id
		ORDER BY ug.name ASC, dc.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list database grants: %w", err)
	}
	defer rows.Close()

	var grants []DatabaseGrantRecord
	for rows.Next() {
		var g DatabaseGrantRecord
		var rawLevel string
		if err := rows.Scan(
			&g.GroupID, &g.GroupName, &g.DatabaseConnectionID,
			&g.ConnectionName, &g.DatabaseName, &g.DBType,
			&rawLevel, &g.GrantedAt, &g.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan grant: %w", err)
		}
		g.AccessLevel = authz.AccessLevel(rawLevel)
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// Mengupdate level akses terhadap database
func (s *Store) UpsertDatabaseGrant(ctx context.Context, actorID, groupID, databaseConnectionID int64, rawLevel string) error {
	level, ok := authz.ParseAccessLevel(rawLevel)
	if !ok || groupID <= 0 || databaseConnectionID <= 0 {
		return fmt.Errorf("%w: invalid group, database connection, or access level", ErrInvalidInput)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO group_database_access (group_id, database_connection_id, access_level, granted_by)
		VALUES ($1, $2, $3, NULLIF($4, 0))
		ON CONFLICT (group_id, database_connection_id) DO UPDATE
		SET access_level = EXCLUDED.access_level,
		    granted_by = EXCLUDED.granted_by,
		    updated_at = NOW()
	`, groupID, databaseConnectionID, string(level), actorID)
	if err != nil {
		return fmt.Errorf("upsert database grant: %w", err)
	}

	_ = s.RecordAuditLog(ctx, &actorID, "access.grant", "group_database_access", fmt.Sprintf("%d:%d", groupID, databaseConnectionID), &databaseConnectionID, map[string]any{
		"group_id":     groupID,
		"access_level": string(level),
	})
	return nil
}

// Merendahkan level akses terhadap database
func (s *Store) RevokeDatabaseGrant(ctx context.Context, actorID, groupID, databaseConnectionID int64) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM group_database_access
		WHERE group_id = $1 AND database_connection_id = $2
	`, groupID, databaseConnectionID)
	if err != nil {
		return fmt.Errorf("revoke database grant: %w", err)
	}

	_ = s.RecordAuditLog(ctx, &actorID, "access.revoke", "group_database_access", fmt.Sprintf("%d:%d", groupID, databaseConnectionID), &databaseConnectionID, map[string]any{
		"group_id": groupID,
	})
	return nil
}

// Menyimpan audit log
func (s *Store) RecordAuditLog(ctx context.Context, actorID *int64, action, resourceType, resourceID string, dbConnID *int64, metadata map[string]any) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		metaBytes = []byte("{}")
	}

	var actorArg any
	if actorID != nil && *actorID > 0 {
		actorArg = *actorID
	}
	var dbConnArg any
	if dbConnID != nil && *dbConnID > 0 {
		dbConnArg = *dbConnID
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO audit_logs (actor_user_id, action, resource_type, resource_id, database_connection_id, metadata)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
	`, actorArg, action, resourceType, resourceID, dbConnArg, string(metaBytes))
	return err
}

// Melakukan list audit log
func (s *Store) ListAuditLogs(ctx context.Context, limit int) ([]AuditLogRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT al.id, al.actor_user_id, COALESCE(u.username, 'system'),
		       al.action, al.resource_type, COALESCE(al.resource_id, ''),
		       al.database_connection_id, al.metadata::text, al.created_at
		FROM audit_logs al
		LEFT JOIN users u ON u.id = al.actor_user_id
		ORDER BY al.created_at DESC, al.id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []AuditLogRecord
	for rows.Next() {
		var rec AuditLogRecord
		var actorID, dbConnID sql.NullInt64
		if err := rows.Scan(
			&rec.ID, &actorID, &rec.ActorUsername,
			&rec.Action, &rec.ResourceType, &rec.ResourceID,
			&dbConnID, &rec.Metadata, &rec.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		if actorID.Valid {
			v := actorID.Int64
			rec.ActorUserID = &v
		}
		if dbConnID.Valid {
			v := dbConnID.Int64
			rec.DatabaseConnectionID = &v
		}
		logs = append(logs, rec)
	}
	return logs, rows.Err()
}
