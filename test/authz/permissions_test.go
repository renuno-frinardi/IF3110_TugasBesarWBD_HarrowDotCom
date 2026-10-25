package authz_test

import (
	"testing"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/authz"
)

func TestParseAccessLevel(t *testing.T) {
	tests := []struct {
		raw   string
		want  authz.AccessLevel
		valid bool
	}{
		{"view_only", authz.LevelViewOnly, true},
		{"view_edit", authz.LevelViewEdit, true},
		{"", authz.LevelNone, false},
		{"admin", authz.LevelNone, false},
		{"VIEW_EDIT", authz.LevelNone, false},
		{" view_only ", authz.LevelNone, false},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, ok := authz.ParseAccessLevel(tc.raw)
			if got != tc.want || ok != tc.valid {
				t.Fatalf("ParseAccessLevel(%q) = (%q,%v), want (%q,%v)", tc.raw, got, ok, tc.want, tc.valid)
			}
		})
	}
}

func TestEffectiveAccessLevel(t *testing.T) {
	tests := []struct {
		name   string
		grants []authz.AccessLevel
		want   authz.AccessLevel
	}{
		{"kosong", nil, authz.LevelNone},
		{"view only", []authz.AccessLevel{authz.LevelViewOnly}, authz.LevelViewOnly},
		{"view edit", []authz.AccessLevel{authz.LevelViewEdit}, authz.LevelViewEdit},
		{"union ambil tertinggi", []authz.AccessLevel{authz.LevelViewOnly, authz.LevelViewEdit}, authz.LevelViewEdit},
		{"urutan terbalik", []authz.AccessLevel{authz.LevelViewEdit, authz.LevelViewOnly}, authz.LevelViewEdit},
		{"none diabaikan", []authz.AccessLevel{authz.LevelNone, authz.LevelViewOnly}, authz.LevelViewOnly},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := authz.EffectiveAccessLevel(tc.grants); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAllowsDatabaseAction(t *testing.T) {
	levels := []authz.AccessLevel{authz.LevelNone, authz.LevelViewOnly, authz.LevelViewEdit}
	// expected[action] = allowed per level (none, view_only, view_edit)
	expected := map[authz.Action][3]bool{
		authz.ActionDBView:           {false, true, true},
		authz.ActionMigrationCreate:  {false, false, true},
		authz.ActionMigrationExecute: {false, false, true},
		authz.ActionPlatformAdmin:    {false, false, false},
		authz.Action("unknown"):      {false, false, false},
	}
	for action, want := range expected {
		for i, level := range levels {
			if got := authz.AllowsDatabaseAction(level, action); got != want[i] {
				t.Errorf("AllowsDatabaseAction(%q,%q) = %v, want %v", level, action, got, want[i])
			}
		}
	}
}
