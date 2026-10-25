package authz

// Representasi aksi yang dijaga dan tersedia pada platform
type Action string

const (
	ActionPlatformAdmin    Action = "platform_admin"
	ActionDBView           Action = "db_view"
	ActionMigrationCreate  Action = "migration_create"
	ActionMigrationExecute Action = "migration_execute"
)

// Representasi tingkatan akses level dari para pengguna
type AccessLevel string

const (
	LevelNone     AccessLevel = ""
	LevelViewOnly AccessLevel = "view_only"
	LevelViewEdit AccessLevel = "view_edit"
)

// Parse akses level dan melakukan normalisasi ke berbagai tipe akses level
func ParseAccessLevel(raw string) (AccessLevel, bool) {
	switch AccessLevel(raw) {
	case LevelViewOnly:
		return LevelViewOnly, true
	case LevelViewEdit:
		return LevelViewEdit, true
	default:
		return LevelNone, false
	}
}

// Melakukan komputasi union akses level dari berbagai grup
func EffectiveAccessLevel(grants []AccessLevel) AccessLevel {
	effective := LevelNone
	for _, g := range grants {
		switch g {
		case LevelViewEdit:
			return LevelViewEdit
		case LevelViewOnly:
			effective = LevelViewOnly
		}
	}
	return effective
}

// Mengecek apakah suatu akses level bisa melakukan akses tertentu pada suatu DB
func AllowsDatabaseAction(level AccessLevel, action Action) bool {
	switch action {
	case ActionDBView:
		return level == LevelViewOnly || level == LevelViewEdit
	case ActionMigrationCreate, ActionMigrationExecute:
		return level == LevelViewEdit
	default:
		return false
	}
}
