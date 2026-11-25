package async

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    10 * time.Second,
	}
}

func (p RetryPolicy) ShouldRetry(currentAttempt int, err error) bool {
	if err == nil {
		return false
	}
	maxAtt := p.MaxAttempts
	if maxAtt < 1 {
		maxAtt = 1
	}
	if currentAttempt >= maxAtt {
		return false
	}
	return IsTransientError(err)
}

func (p RetryPolicy) NextBackoff(attempt int) time.Duration {
	base := p.BaseDelay
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	maxD := p.MaxDelay
	if maxD <= 0 {
		maxD = 10 * time.Second
	}
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 10 {
		shift = 10
	}
	delay := base * time.Duration(1<<shift)
	if delay > maxD || delay <= 0 {
		return maxD
	}
	return delay
}

func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCancelUnsafe) || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", // serialization_failure
			"40P01", // deadlock_detected
			"55P03", // lock_not_available
			"53300", // too_many_connections
			"57P03", // cannot_connect_now
			"08000", // connection_exception
			"08001", // sqlclient_unable_to_establish_sqlconnection
			"08003", // connection_does_not_exist
			"08006": // connection_failure
			return true
		default:
			return false
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return true
	}

	msg := strings.ToLower(err.Error())
	for _, transientMarker := range []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"unexpected eof",
		"failed to connect to target database",
		"temporary failure",
		"deadlock detected",
		"could not serialize access",
	} {
		if strings.Contains(msg, transientMarker) {
			return true
		}
	}
	return false
}

func IsCommitUncertain(target *pgx.Conn, commitErr error) bool {
	if commitErr == nil {
		return false
	}
	if errors.Is(commitErr, pgx.ErrTxCommitRollback) {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(commitErr, &pgErr) && pgErr.Severity == "ERROR" {
		if target != nil && !target.IsClosed() && target.PgConn().TxStatus() == 'I' {
			return false
		}
	}
	return true
}
