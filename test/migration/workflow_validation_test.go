package migration

import (
	"errors"
	"testing"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
)

func TestValidateWorkflowSQL(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		upSQL       string
		downSQL     string
		cfg         migration.BatchConfig
		expectError bool
	}{
		{
			name:  "normal SQL syntax",
			mode:  migration.ModeNormal,
			upSQL: "UPDATE items SET processed = TRUE;",
		},
		{
			name:        "invalid normal SQL syntax",
			mode:        migration.ModeNormal,
			upSQL:       "UPDATE items SET processed = WHERE id = 1;",
			expectError: true,
		},
		{
			name:        "invalid down SQL syntax",
			mode:        migration.ModeNormal,
			upSQL:       "UPDATE items SET processed = TRUE;",
			downSQL:     "UPDATE items SET processed = WHERE id = 1;",
			expectError: true,
		},
		{
			name:  "batch placeholders",
			mode:  migration.ModeBatch,
			cfg:   migration.BatchConfig{Column: "id", Size: 500},
			upSQL: "UPDATE items SET processed = TRUE WHERE {{batch_column}} IN (SELECT {{batch_column}} FROM items WHERE {{batch_column}} > {{last_id}} ORDER BY {{batch_column}} LIMIT {{batch_size}});",
			downSQL: "UPDATE items SET processed = FALSE WHERE {{batch_column}} BETWEEN " + "{{range_from}} AND {{range_to}};",
		},
		{
			name:        "invalid batch SQL syntax",
			mode:        migration.ModeBatch,
			cfg:         migration.BatchConfig{Column: "id", Size: 500},
			upSQL:       "UPDATE items SET processed = WHERE {{batch_column}} > {{last_id}} LIMIT {{batch_size}};",
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateWorkflowSQL(test.mode, test.upSQL, test.downSQL, test.cfg)
			if test.expectError {
				if !errors.Is(err, InvalidInput) {
					t.Fatalf("expected invalid SQL error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected valid SQL, got %v", err)
			}
		})
	}
}