package util

import (
	"context"
	"time"
)

// bootstrapRetryStatuses 是 bootstrap 值得重试的上游状态码。
//
// 403 通常是 Cloudflare 挑战（cf_clearance 失效或指纹不匹配），429 是上游限流；
// 两者都可能是瞬时的，短暂退避后重试比立刻失败更划算。
// 重试仍失败则如实上抛，由调用方决定是否换账号。
var bootstrapRetryStatuses = map[int]struct{}{
	403: {},
	429: {},
}

// IsRetryableBootstrapStatus 判断该状态码是否值得重试。
func IsRetryableBootstrapStatus(status int) bool {
	_, ok := bootstrapRetryStatuses[status]
	return ok
}

const (
	// MaxBootstrapAttempts 是 bootstrap 的最大尝试次数。
	MaxBootstrapAttempts = 3
	// BootstrapRetryBaseDelay 是退避基数：第 n 次失败后等待 n × 基数。
	BootstrapRetryBaseDelay = 800 * time.Millisecond
)

// RetryBootstrap 按统一策略重试一次 bootstrap 请求。
//
// fn 的 attempt 从 1 开始，返回 (err, retryable)；retryable 为 false 时立即停止。
//
// 这是生图（internal/backend）与账号刷新（internal/service）共用的唯一实现。
// 两条链路此前各写一份，刷新链路漏了重试，导致同一个瞬时 403 在生图能被重试吸收、
// 在刷新却直接上报失败——批量刷新因此大面积误报「碰到 Cloudflare」。
func RetryBootstrap(ctx context.Context, fn func(attempt int) (error, bool)) error {
	var lastErr error
	for attempt := 1; attempt <= MaxBootstrapAttempts; attempt++ {
		err, retryable := fn(attempt)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable || attempt == MaxBootstrapAttempts {
			break
		}
		select {
		case <-time.After(time.Duration(attempt) * BootstrapRetryBaseDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}
