// Package observability supplies safe metadata shared by transport boundaries.
package observability

import (
	"context"
	"errors"
	"log/slog"
	"net"
)

type correlationKey struct{}

type correlation struct {
	name  string
	value any
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, correlation{"request_id", id})
}

func WithUpdateID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, correlationKey{}, correlation{"update_id", id})
}

func Logger(ctx context.Context, component, operation string) *slog.Logger {
	logger := slog.Default().With("component", component, "operation", operation)
	if value, ok := ctx.Value(correlationKey{}).(correlation); ok {
		logger = logger.With(value.name, value.value)
	}
	return logger
}

// Category never formats an error: driver and transport messages can contain
// credentials, URLs, SQL, or expense contents, including at DEBUG level.
func Category(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return "timeout"
		}
		return "network"
	}

	var databaseError interface{ SQLState() string }
	if errors.As(err, &databaseError) {
		switch databaseError.SQLState() {
		case "40001":
			return "serialization_failure"
		case "40P01":
			return "deadlock"
		default:
			return "database"
		}
	}
	return "application"
}
