package async

import (
	"testing"

	"github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom/internal/migration"
)

func TestDecideNormalIdempotency(t *testing.T) {
	if got := DecideNormalIdempotency(migration.ExecPending, false); got != NormalDecisionExecute {
		t.Fatalf("expected fresh execution to run SQL, got %v", got)
	}
	if got := DecideNormalIdempotency(migration.ExecSuccess, false); got != NormalDecisionSkipCompleted {
		t.Fatalf("expected already-succeeded metadata status to skip SQL, got %v", got)
	}
	if got := DecideNormalIdempotency(migration.ExecRunning, true); got != NormalDecisionSkipCompleted {
		t.Fatalf("expected existing target ledger proof after uncertain commit to skip duplicate SQL, got %v", got)
	}
}

func TestDecideBatchIdempotency(t *testing.T) {
	if got := DecideBatchIdempotency(nil, false); got != BatchDecisionExecute {
		t.Fatalf("expected uncommitted batch to execute, got %v", got)
	}

	committedTargetCP := &TargetBatchCheckpoint{
		ExecutionID:  99,
		BatchNo:      2,
		CursorFrom:   "101",
		CursorTo:     "200",
		RowsAffected: 100,
		Status:       "committed",
	}
	if got := DecideBatchIdempotency(committedTargetCP, false); got != BatchDecisionSyncMetadataOnly {
		t.Fatalf("expected target-committed/metadata-missing batch to sync metadata only, got %v", got)
	}

	if got := DecideBatchIdempotency(committedTargetCP, true); got != BatchDecisionSkipAlreadyRecorded {
		t.Fatalf("expected batch recorded in both target and metadata to be skipped, got %v", got)
	}
}
