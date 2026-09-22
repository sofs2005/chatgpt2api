package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// textAttachmentCacheTTL bounds how long an uploaded attachment stays reusable.
// It only has to cover the token-switch retries of one user request: a single
// upstream attempt may hang for the 5-minute HTTP client timeout before the next
// attempt starts, so 10 minutes covers a full attempt plus its retry. Keeping it
// short also bounds the window in which an admin-issued upstream file deletion
// could leave a cached file_id pointing at a file that no longer exists.
const textAttachmentCacheTTL = 10 * time.Minute

// TextAttachmentCache reuses uploaded attachments across token-switch retries.
// Uploaded files live in a specific account's library, so the cache key carries
// the account email: a retry that lands on another account must upload again.
type TextAttachmentCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]textAttachmentCacheEntry
}

type textAttachmentCacheEntry struct {
	ref       TextAttachmentRef
	expiresAt time.Time
}

func NewTextAttachmentCache(ttl time.Duration) *TextAttachmentCache {
	return &TextAttachmentCache{ttl: ttl, entries: map[string]textAttachmentCacheEntry{}}
}

// TextAttachmentCacheTTL 暴露默认 TTL，供装配层构造共享缓存。
func TextAttachmentCacheTTL() time.Duration {
	return textAttachmentCacheTTL
}

func (c *TextAttachmentCache) Get(key string) (TextAttachmentRef, bool) {
	if c == nil || c.ttl <= 0 || key == "" {
		return TextAttachmentRef{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return TextAttachmentRef{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return TextAttachmentRef{}, false
	}
	return entry.ref, true
}

func (c *TextAttachmentCache) Put(key string, ref TextAttachmentRef) {
	if c == nil || c.ttl <= 0 || key == "" || ref.FileID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = textAttachmentCacheEntry{ref: ref, expiresAt: time.Now().Add(c.ttl)}
}

// textAttachmentCacheKey identifies an uploaded attachment by the account that
// owns it plus the exact file name and content.
func textAttachmentCacheKey(accountEmail, filename, content string) string {
	if accountEmail == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(accountEmail + "\x00" + filename + "\x00" + content))
	return hex.EncodeToString(sum[:])
}
