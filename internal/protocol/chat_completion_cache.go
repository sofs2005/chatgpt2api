package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

const defaultChatCompletionCacheTTL = 2 * time.Second

// ChatCompletionCache stores short-lived text completion results and folds
// concurrent requests for the same normalized key into a single fetch.
type ChatCompletionCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	entries  map[string]chatCompletionCacheEntry
	inflight map[string]*chatCompletionCacheCall
}

type chatCompletionCacheEntry struct {
	value     string
	expiresAt time.Time
}

type chatCompletionCacheCall struct {
	wg    sync.WaitGroup
	value string
	err   error
}

func NewChatCompletionCache(ttl time.Duration) *ChatCompletionCache {
	return &ChatCompletionCache{ttl: ttl, entries: map[string]chatCompletionCacheEntry{}, inflight: map[string]*chatCompletionCacheCall{}}
}

func (c *ChatCompletionCache) Do(key string, fetch func() (string, error)) (string, error) {
	if c == nil || c.ttl <= 0 || key == "" {
		return fetch()
	}
	now := time.Now()
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]chatCompletionCacheEntry{}
	}
	if entry, ok := c.entries[key]; ok {
		if now.Before(entry.expiresAt) {
			value := entry.value
			c.mu.Unlock()
			return value, nil
		}
		delete(c.entries, key)
	}
	if c.inflight == nil {
		c.inflight = map[string]*chatCompletionCacheCall{}
	}
	if call := c.inflight[key]; call != nil {
		c.mu.Unlock()
		call.wg.Wait()
		return call.value, call.err
	}
	call := &chatCompletionCacheCall{}
	call.wg.Add(1)
	c.inflight[key] = call
	c.mu.Unlock()

	value, err := fetch()

	c.mu.Lock()
	call.value = value
	call.err = err
	if err == nil {
		c.entries[key] = chatCompletionCacheEntry{value: value, expiresAt: time.Now().Add(c.ttl)}
	}
	delete(c.inflight, key)
	call.wg.Done()
	c.mu.Unlock()
	return value, err
}

func (e *Engine) chatCompletionCache() *ChatCompletionCache {
	if e == nil {
		return NewChatCompletionCache(defaultChatCompletionCacheTTL)
	}
	e.chatCompletionCacheMu.Lock()
	defer e.chatCompletionCacheMu.Unlock()
	if e.ChatCompletionCache == nil {
		e.ChatCompletionCache = NewChatCompletionCache(defaultChatCompletionCacheTTL)
	}
	return e.ChatCompletionCache
}

func (e *Engine) collectTextWithCache(ctxKey string, fetch func() (string, error)) (string, error) {
	return e.chatCompletionCache().Do(ctxKey, fetch)
}

func textCompletionCacheKey(request ConversationRequest) string {
	payload := map[string]any{
		"model":       request.Model,
		"messages":    NormalizeMessages(request.Messages, nil),
		"tools":       request.Tools,
		"tool_choice": request.ToolChoice,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
