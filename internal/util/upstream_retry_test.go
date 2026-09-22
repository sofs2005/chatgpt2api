package util

import (
	"context"
	"errors"
	"testing"
)

func TestIsRetryableBootstrapStatus(t *testing.T) {
	// 403（Cloudflare 挑战）与 429（限流）都可能是瞬时的，值得重试。
	for _, status := range []int{403, 429} {
		if !IsRetryableBootstrapStatus(status) {
			t.Fatalf("IsRetryableBootstrapStatus(%d) = false, want retryable", status)
		}
	}
	// 其余状态重试没有意义：404 不会因为重试变成 200。
	for _, status := range []int{0, 200, 401, 404, 500} {
		if IsRetryableBootstrapStatus(status) {
			t.Fatalf("IsRetryableBootstrapStatus(%d) = true, want non-retryable", status)
		}
	}
}

func TestRetryBootstrapSucceedsAfterTransientFailure(t *testing.T) {
	attempts := 0
	err := RetryBootstrap(context.Background(), func(attempt int) (error, bool) {
		attempts++
		if attempt == 1 {
			return errors.New("transient challenge"), true
		}
		return nil, false
	})
	if err != nil {
		t.Fatalf("RetryBootstrap() error = %v, want success on the retry", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRetryBootstrapStopsOnNonRetryable(t *testing.T) {
	attempts := 0
	want := errors.New("not found")
	err := RetryBootstrap(context.Background(), func(attempt int) (error, bool) {
		attempts++
		return want, false
	})
	if !errors.Is(err, want) {
		t.Fatalf("RetryBootstrap() error = %v, want the original error", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 for a non-retryable failure", attempts)
	}
}

func TestRetryBootstrapExhaustsAttempts(t *testing.T) {
	attempts := 0
	err := RetryBootstrap(context.Background(), func(attempt int) (error, bool) {
		attempts++
		return errors.New("still challenged"), true
	})
	if err == nil {
		t.Fatal("RetryBootstrap() error = nil, want the last failure reported")
	}
	if attempts != MaxBootstrapAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, MaxBootstrapAttempts)
	}
}

func TestRetryBootstrapHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := RetryBootstrap(ctx, func(attempt int) (error, bool) {
		attempts++
		// 首次失败后取消：不应继续重试，且应返回 ctx 的错误。
		cancel()
		return errors.New("transient"), true
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RetryBootstrap() error = %v, want context.Canceled", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 before the cancellation took effect", attempts)
	}
}
