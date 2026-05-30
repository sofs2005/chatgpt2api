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

func TestSessionRefresherReturnsValidatedUser(t *testing.T) {
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z","user":{"id":"user-123","email":"user@example.com","name":"User Name"}}`)),
		}, nil
	})

	result, err := refresher.RefreshSession(context.Background(), "old-access", "old-session")
	if err != nil {
		t.Fatalf("RefreshSession() error = %v", err)
	}
	if result.AccessToken != "new-access" || result.SessionToken != "new-session" || result.Expires != "2026-05-12T00:00:00Z" {
		t.Fatalf("RefreshSession() tokens = %#v", result)
	}
	if result.User.ID != "user-123" || result.User.Email != "user@example.com" || result.User.Name != "User Name" {
		t.Fatalf("RefreshSession() user = %#v", result.User)
	}
}

func TestSessionRefresherSendsBrowserCookiesAndHeaders(t *testing.T) {
	refresher := NewSessionRefresher(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("User-Agent"); got != "Browser UA" {
			t.Fatalf("User-Agent = %q, want Browser UA", got)
		}
		if got := req.Header.Get("Sec-Ch-Ua"); got != `"Chromium";v="145"` {
			t.Fatalf("Sec-Ch-Ua = %q", got)
		}
		if got := req.Header.Get("OAI-Device-Id"); got != "device-1" {
			t.Fatalf("OAI-Device-Id = %q", got)
		}
		for name, want := range map[string]string{
			"__Secure-next-auth.session-token": "session-cookie",
			"cf_clearance":                     "cf-cookie",
			"__cf_bm":                          "bm-cookie",
			"oai-did":                          "did-cookie",
		} {
			cookie, err := req.Cookie(name)
			if err != nil || cookie.Value != want {
				t.Fatalf("cookie %s = %#v err %v, want %q", name, cookie, err, want)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accessToken":"new-access","sessionToken":"new-session","expires":"2026-05-12T00:00:00Z"}`)),
		}, nil
	})

	_, err := refresher.RefreshSessionWithContext(context.Background(), "old-access", "session-cookie", SessionRefreshContext{
		Cookies: map[string]string{
			"cf_clearance": "cf-cookie",
			"__cf_bm":      "bm-cookie",
			"oai-did":      "did-cookie",
		},
		Headers: map[string]string{
			"User-Agent":    "Browser UA",
			"Sec-Ch-Ua":     `"Chromium";v="145"`,
			"OAI-Device-Id": "device-1",
		},
	})
	if err != nil {
		t.Fatalf("RefreshSessionWithContext() error = %v", err)
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
