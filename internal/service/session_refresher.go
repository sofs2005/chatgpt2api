package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// SessionRefresher 使用 uTLS 调用 /api/auth/session 刷新 token
type SessionRefresher struct {
	mu        sync.Mutex
	inFlight  map[string]*refreshCall // key: access_token, 去重
	semaphore chan struct{}           // 并发控制 (max 5 concurrent)
	httpDo    func(req *http.Request) (*http.Response, error)
}

type refreshCall struct {
	done   chan struct{}
	result refreshResult
}

type SessionRefreshData struct {
	AccessToken  string
	SessionToken string
	Expires      string
	User         SessionRefreshUser
}

type SessionRefreshUser struct {
	ID    string
	Name  string
	Email string
}

type refreshResult struct {
	accessToken    string
	sessionToken   string
	sessionExpires string
	user           SessionRefreshUser
	err            error
}

const (
	maxConcurrentRefreshes = 5
	refreshTimeout         = 15 * time.Second
	sessionEndpoint        = "https://chatgpt.com/api/auth/session"
)

func NewSessionRefresher(httpDo func(req *http.Request) (*http.Response, error)) *SessionRefresher {
	return &SessionRefresher{
		inFlight:  make(map[string]*refreshCall),
		semaphore: make(chan struct{}, maxConcurrentRefreshes),
		httpDo:    httpDo,
	}
}

// RefreshToken 使用 session_token 刷新 access_token
// 如果同一 token 正在刷新中，等待并返回结果（去重）
func (r *SessionRefresher) RefreshToken(ctx context.Context, accessToken, sessionToken string) (newAccessToken, newSessionToken, newExpires string, err error) {
	result, err := r.RefreshSession(ctx, accessToken, sessionToken)
	return result.AccessToken, result.SessionToken, result.Expires, err
}

func (r *SessionRefresher) RefreshSession(ctx context.Context, accessToken, sessionToken string) (SessionRefreshData, error) {
	if sessionToken == "" {
		return SessionRefreshData{}, fmt.Errorf("session_token is empty")
	}

	// 去重：检查是否已有进行中的刷新
	r.mu.Lock()
	if call, ok := r.inFlight[accessToken]; ok {
		r.mu.Unlock()
		select {
		case <-call.done:
			return call.result.sessionData(), call.result.err
		case <-ctx.Done():
			return SessionRefreshData{}, ctx.Err()
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	r.inFlight[accessToken] = call
	r.mu.Unlock()

	finish := func(result refreshResult) (SessionRefreshData, error) {
		call.result = result
		close(call.done)
		r.mu.Lock()
		delete(r.inFlight, accessToken)
		r.mu.Unlock()
		return result.sessionData(), result.err
	}

	// 获取信号量
	select {
	case r.semaphore <- struct{}{}:
		defer func() { <-r.semaphore }()
	case <-ctx.Done():
		return finish(refreshResult{err: ctx.Err()})
	}

	// 执行刷新
	return finish(r.doRefresh(ctx, sessionToken))
}

func (r refreshResult) sessionData() SessionRefreshData {
	return SessionRefreshData{
		AccessToken:  r.accessToken,
		SessionToken: r.sessionToken,
		Expires:      r.sessionExpires,
		User:         r.user,
	}
}

func (r *SessionRefresher) doRefresh(ctx context.Context, sessionToken string) refreshResult {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sessionEndpoint, nil)
	if err != nil {
		return refreshResult{err: fmt.Errorf("create request: %w", err)}
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{
		Name:     "__Secure-next-auth.session-token",
		Value:    sessionToken,
		Domain:   ".chatgpt.com",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	resp, err := r.httpDo(req)
	if err != nil {
		return refreshResult{err: fmt.Errorf("http request: %w", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return refreshResult{err: fmt.Errorf("read body: %w", err)}
	}

	if resp.StatusCode != http.StatusOK {
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return refreshResult{err: fmt.Errorf("session endpoint returned %d: %s", resp.StatusCode, preview)}
	}

	var session struct {
		AccessToken  string `json:"accessToken"`
		Expires      string `json:"expires"`
		SessionToken string `json:"sessionToken"`
		User         struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		return refreshResult{err: fmt.Errorf("parse session response: %w", err)}
	}

	if session.AccessToken == "" {
		return refreshResult{err: fmt.Errorf("session response missing accessToken")}
	}

	// 如果响应中没有新的 sessionToken，保留旧值
	newSessionToken := session.SessionToken
	if newSessionToken == "" {
		newSessionToken = sessionToken
	}

	return refreshResult{
		accessToken:    session.AccessToken,
		sessionToken:   newSessionToken,
		sessionExpires: session.Expires,
		user: SessionRefreshUser{
			ID:    session.User.ID,
			Name:  session.User.Name,
			Email: session.User.Email,
		},
	}
}

// IsRefreshing 返回指定 token 是否正在刷新中
func (r *SessionRefresher) IsRefreshing(accessToken string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.inFlight[accessToken]
	return ok
}

// TryRefreshAsync 异步触发刷新（fire-and-forget），用于实时请求场景
// 返回 true 表示已提交刷新任务
func (r *SessionRefresher) TryRefreshAsync(accessToken, sessionToken string) bool {
	if sessionToken == "" {
		return false
	}
	r.mu.Lock()
	if _, ok := r.inFlight[accessToken]; ok {
		r.mu.Unlock()
		return true // 已在刷新中
	}
	r.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		r.RefreshToken(ctx, accessToken, sessionToken)
	}()
	return true
}
