package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
)

type ctxKey string

const (
	correlationIDKey ctxKey = "harrow_correlation_id"
	actorKey         ctxKey = "harrow_actor"
	databaseKey      ctxKey = "harrow_database"
)

func NewCorrelationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "corr-fallback"
	}
	return hex.EncodeToString(b[:])
}

func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationIDKey, id)
}

func CorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(correlationIDKey).(string)
	return v
}

func WithActor(ctx context.Context, actor string) context.Context {
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, actorKey, actor)
}

func WithActorID(ctx context.Context, userID int) context.Context {
	if userID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, actorKey, strconv.Itoa(userID))
}

func ActorFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(actorKey).(string)
	return v
}

func WithDatabase(ctx context.Context, db string) context.Context {
	if db == "" {
		return ctx
	}
	return context.WithValue(ctx, databaseKey, db)
}

func WithDatabaseID(ctx context.Context, dbID int) context.Context {
	if dbID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, databaseKey, strconv.Itoa(dbID))
}

func DatabaseFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(databaseKey).(string)
	return v
}
