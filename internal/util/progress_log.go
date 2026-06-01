package util

import (
	"context"
	"strings"
)

type progressLogContextKey struct{}

func WithProgressLogger(ctx context.Context, logger func(string)) context.Context {
	if ctx == nil || logger == nil {
		return ctx
	}
	return context.WithValue(ctx, progressLogContextKey{}, logger)
}

func LogProgress(ctx context.Context, message string) {
	message = strings.TrimSpace(message)
	if ctx == nil || message == "" {
		return
	}
	logger, _ := ctx.Value(progressLogContextKey{}).(func(string))
	if logger != nil {
		logger(message)
	}
}
