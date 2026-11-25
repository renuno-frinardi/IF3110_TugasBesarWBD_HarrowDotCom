package async

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRetryPolicyClassificationAndBackoff(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   200 * time.Millisecond,
		MaxDelay:    1500 * time.Millisecond,
	}

	transientErrors := []error{
		&pgconn.PgError{Code: "40001", Message: "could not serialize access"},
		&pgconn.PgError{Code: "40P01", Message: "deadlock detected"},
		&pgconn.PgError{Code: "55P03", Message: "canceling statement due to lock timeout"},
		&pgconn.PgError{Code: "08006", Message: "connection failure"},
		io.ErrUnexpectedEOF,
		context.DeadlineExceeded,
		errors.New("read tcp: connection reset by peer"),
	}
	for _, err := range transientErrors {
		if !IsTransientError(err) {
			t.Errorf("expected %v to be classified as transient", err)
		}
		if !policy.ShouldRetry(1, err) || !policy.ShouldRetry(2, err) {
			t.Errorf("expected attempts 1 and 2 to retry for transient error %v", err)
		}
		if policy.ShouldRetry(3, err) {
			t.Errorf("expected attempt 3 (at MaxAttempts=3) NOT to retry for %v", err)
		}
	}

	permanentErrors := []error{
		&pgconn.PgError{Code: "42601", Message: "syntax error at or near \"FRO\""},
		&pgconn.PgError{Code: "42P01", Message: "relation \"missing\" does not exist"},
		&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"},
		context.Canceled,
		ErrCancelUnsafe,
	}
	for _, err := range permanentErrors {
		if IsTransientError(err) {
			t.Errorf("expected %v NOT to be classified as transient", err)
		}
		if policy.ShouldRetry(1, err) {
			t.Errorf("expected permanent error %v NOT to retry even on attempt 1", err)
		}
	}

	if d := policy.NextBackoff(1); d != 200*time.Millisecond {
		t.Errorf("attempt 1 backoff = %v, want 200ms", d)
	}
	if d := policy.NextBackoff(2); d != 400*time.Millisecond {
		t.Errorf("attempt 2 backoff = %v, want 400ms", d)
	}
	if d := policy.NextBackoff(3); d != 800*time.Millisecond {
		t.Errorf("attempt 3 backoff = %v, want 800ms", d)
	}
	if d := policy.NextBackoff(5); d != 1500*time.Millisecond {
		t.Errorf("attempt 5 backoff = %v, want capped 1500ms", d)
	}
}

func TestIsCommitUncertain(t *testing.T) {
	if IsCommitUncertain(nil, nil) {
		t.Fatal("nil error must not be uncertain")
	}
	if IsCommitUncertain(nil, pgx.ErrTxCommitRollback) {
		t.Fatal("ErrTxCommitRollback is a definitive rollback, not uncertain")
	}
	if !IsCommitUncertain(nil, io.ErrUnexpectedEOF) {
		t.Fatal("network drop during commit must be treated as uncertain")
	}
}
