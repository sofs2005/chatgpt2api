package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionRefresherRejectsEmptySessionToken(t *testing.T) {
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("httpDo should not be called for empty session token")
		return nil, nil
	})

	_, _, _, err := refresher.RefreshToken(context.Background(), "access-token", "")
	if err == nil || !strings.Contains(err.Error(), "session_token is empty") {
		t.Fatalf("expected empty session token error, got %v", err)
	}
}

func TestSessionRefresherDeduplicatesConcurrentRefreshes(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})

	const waiters = 5
	var wg sync.WaitGroup
	results := make(chan refreshResult, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			accessToken, sessionToken, expires, err := refresher.RefreshToken(context.Background(), "old-access", "old-session")
			results <- refreshResult{
				accessToken:    accessToken,
				sessionToken:   sessionToken,
				sessionExpires: expires,
				err:            err,
			}
		}()
	}

	waitForCondition(t, func() bool {
		return atomic.LoadInt32(&calls) == 1 && refresher.IsRefreshing("old-access")
	})
	close(release)
	wg.Wait()
	close(results)

	if calls := atomic.LoadInt32(&calls); calls != 1 {
		t.Fatalf("expected one upstream refresh, got %d", calls)
	}
	if refresher.IsRefreshing("old-access") {
		t.Fatalf("refresh should be cleared after completion")
	}
	for result := range results {
		if result.err != nil {
			t.Fatalf("refresh returned error: %v", result.err)
		}
		if result.accessToken != "new-access" || result.sessionToken != "new-session" || result.sessionExpires != "2026-05-12T00:00:00Z" {
			t.Fatalf("unexpected refresh result: %#v", result)
		}
	}
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition was not met before deadline")
}
