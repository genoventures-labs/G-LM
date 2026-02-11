package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type ctxKey string

const traceIDKey ctxKey = "trace_id"

func NewTraceID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

func TraceID(ctx context.Context) string {
	v, _ := ctx.Value(traceIDKey).(string)
	return v
}
