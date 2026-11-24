package migration

import "testing"

func TestNormalizeBatchConfig(t *testing.T) {
	if _, err := normalizeBatchConfig(ModeSingle, BatchConfig{Column: "ignored", Size: 99}); err != nil {
		t.Fatalf("single mode should normalize: %v", err)
	}
	cfg, err := normalizeBatchConfig(ModeBatch, BatchConfig{Column: "id", Size: 500})
	if err != nil {
		t.Fatalf("valid batch config rejected: %v", err)
	}
	if cfg.Column != "id" || cfg.Size != 500 {
		t.Fatalf("unexpected batch config: %#v", cfg)
	}
	if _, err := normalizeBatchConfig(ModeBatch, BatchConfig{Column: "id;drop", Size: 500}); err == nil {
		t.Fatal("unsafe batch column should be rejected")
	}
}

func TestValidateBatchSQLPlaceholders(t *testing.T) {
	up := "UPDATE users SET active=true WHERE {{batch_column}}>{{last_id}} LIMIT {{batch_size}}"
	down := "UPDATE users SET active=false WHERE {{batch_column}} BETWEEN {{range_from}} AND {{range_to}}"
	if err := validateBatchSQL(ModeBatch, up, down); err != nil {
		t.Fatalf("valid batch SQL rejected: %v", err)
	}
	if err := validateBatchSQL(ModeBatch, "UPDATE users SET active=true", down); err == nil {
		t.Fatal("batch up SQL without required placeholders should be rejected")
	}
}