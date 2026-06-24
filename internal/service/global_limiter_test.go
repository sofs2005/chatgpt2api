package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGlobalLimiter_ZeroLimitIsUnlimited(t *testing.T) {
	gl := NewGlobalLimiter(0)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := gl.Acquire(context.Background())
			if err != nil {
				t.Errorf("Acquire with limit=0 should not fail: %v", err)
				return
			}
			release()
		}()
	}
	wg.Wait()
}

func TestGlobalLimiter_BlocksWhenAtCapacity(t *testing.T) {
	gl := NewGlobalLimiter(2)
	ctx := context.Background()

	r1, err := gl.Acquire(ctx)
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}
	r2, err := gl.Acquire(ctx)
	if err != nil {
		t.Fatalf("second Acquire failed: %v", err)
	}

	// Third acquire should block
	blocked := make(chan struct{})
	go func() {
		r3, err := gl.Acquire(ctx)
		if err != nil {
			t.Errorf("third Acquire failed: %v", err)
			return
		}
		close(blocked)
		r3()
	}()

	select {
	case <-blocked:
		t.Fatal("third Acquire should have blocked but did not")
	case <-time.After(200 * time.Millisecond):
		// expected: still blocked
	}

	// Release one slot — third acquire should now proceed
	r1()
	select {
	case <-blocked:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("third Acquire did not unblock after release")
	}
	r2()
}

func TestGlobalLimiter_ContextCancellation(t *testing.T) {
	gl := NewGlobalLimiter(1)
	ctx := context.Background()

	release, err := gl.Acquire(ctx)
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := gl.Acquire(cancelCtx)
		done <- err
	}()

	// Give the goroutine time to start waiting
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected context cancellation error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not return after context cancellation")
	}
	release()
}

func TestGlobalLimiter_SetLimitDynamic(t *testing.T) {
	gl := NewGlobalLimiter(1)
	ctx := context.Background()

	r1, err := gl.Acquire(ctx)
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}

	// Second should block at limit=1
	blocked := make(chan struct{})
	go func() {
		r2, err := gl.Acquire(ctx)
		if err != nil {
			return
		}
		close(blocked)
		r2()
	}()

	time.Sleep(100 * time.Millisecond)

	// Raise limit to 2 — blocked acquire should proceed
	gl.SetLimit(2)
	select {
	case <-blocked:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not unblock after SetLimit increase")
	}
	r1()
}

func TestGlobalLimiter_DoubleReleaseSafe(t *testing.T) {
	gl := NewGlobalLimiter(1)
	ctx := context.Background()

	release, err := gl.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	release()
	release() // double release should be safe

	// Should still be able to acquire
	release2, err := gl.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire after double-release failed: %v", err)
	}
	release2()
}

func TestGlobalLimiter_ConcurrentStress(t *testing.T) {
	gl := NewGlobalLimiter(5)
	ctx := context.Background()
	const goroutines = 50
	const iterations = 20

	var active int64
	var maxActive int64
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				release, err := gl.Acquire(ctx)
				if err != nil {
					t.Errorf("Acquire failed: %v", err)
					return
				}
				cur := atomic.AddInt64(&active, 1)
				mu.Lock()
				if cur > maxActive {
					maxActive = cur
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				atomic.AddInt64(&active, -1)
				release()
			}
		}()
	}
	wg.Wait()

	if maxActive > 5 {
		t.Errorf("max concurrent = %d, want <= 5", maxActive)
	}
}
