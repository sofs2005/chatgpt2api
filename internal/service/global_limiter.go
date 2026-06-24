package service

import (
	"context"
	"sync"
	"time"
)

// GlobalLimiter enforces a system-wide concurrency limit.
// Requests that exceed the limit block until a slot becomes available
// or the context is cancelled. A limit <= 0 means unlimited.
type GlobalLimiter struct {
	mu      sync.Mutex
	cond    *sync.Cond
	running int
	limit   int
}

// NewGlobalLimiter creates a new global concurrency limiter.
// A limit <= 0 means no limit is enforced.
func NewGlobalLimiter(limit int) *GlobalLimiter {
	gl := &GlobalLimiter{limit: limit}
	gl.cond = sync.NewCond(&gl.mu)
	return gl
}

// SetLimit updates the concurrency limit dynamically.
// This will wake up any waiting goroutines so they can re-evaluate.
func (gl *GlobalLimiter) SetLimit(limit int) {
	gl.mu.Lock()
	defer gl.mu.Unlock()
	gl.limit = limit
	gl.cond.Broadcast()
}

// Acquire blocks until a slot is available or ctx is cancelled.
// Returns a release function that must be called when the request completes.
// The release function is idempotent and safe to call multiple times.
func (gl *GlobalLimiter) Acquire(ctx context.Context) (func(), error) {
	gl.mu.Lock()
	defer gl.mu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if gl.limit <= 0 || gl.running < gl.limit {
			gl.running++
			released := false
			return func() {
				gl.mu.Lock()
				defer gl.mu.Unlock()
				if released {
					return
				}
				released = true
				gl.running--
				gl.cond.Broadcast()
			}, nil
		}

		// Periodic broadcast to re-check context cancellation
		timer := time.AfterFunc(100*time.Millisecond, func() {
			gl.mu.Lock()
			gl.cond.Broadcast()
			gl.mu.Unlock()
		})
		gl.cond.Wait()
		timer.Stop()
	}
}
