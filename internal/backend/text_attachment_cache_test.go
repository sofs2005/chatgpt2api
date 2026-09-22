package backend

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt2api/internal/contextoffload"
)

// countingUploadServer serves the three-call upload flow and counts create calls.
func countingUploadServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var creates int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == textFileCreatePath:
			fileID := "file_" + string(rune('a'+atomic.AddInt32(&creates, 1)-1))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":     "success",
				"upload_url": server.URL + "/blob/raw",
				"file_id":    fileID,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/blob/raw":
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == textFileProcessPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"event":"file.indexing.completed"}`+"\n\n")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	return server, &creates
}

type staticAccountLookup struct {
	mu      sync.Mutex
	account map[string]any
}

func (l *staticAccountLookup) GetAccount(accessToken string) map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.account
}

func (l *staticAccountLookup) set(account map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.account = account
}

func historyFile() contextoffload.File {
	return contextoffload.File{Filename: "history.txt", ContentType: "text/plain", Text: "long history"}
}

func TestUploadTextContextFilesReusesCachedAttachmentForSameAccount(t *testing.T) {
	server, creates := countingUploadServer(t)
	defer server.Close()

	lookup := &staticAccountLookup{account: map[string]any{"email": "same@example.com"}}
	cache := NewTextAttachmentCache(time.Minute)

	first := &Client{BaseURL: server.URL, AccessToken: "token-a", httpClient: server.Client(), lookup: lookup}
	first.SetTextAttachmentCache(cache)
	refs, err := first.uploadTextContextFiles(t.Context(), []contextoffload.File{historyFile()}, ChatRequirements{}, time.Second)
	if err != nil {
		t.Fatalf("first upload error = %v", err)
	}

	// A token-switch retry builds a new client for the same account.
	second := &Client{BaseURL: server.URL, AccessToken: "token-b", httpClient: server.Client(), lookup: lookup}
	second.SetTextAttachmentCache(cache)
	retryRefs, err := second.uploadTextContextFiles(t.Context(), []contextoffload.File{historyFile()}, ChatRequirements{}, time.Second)
	if err != nil {
		t.Fatalf("retry upload error = %v", err)
	}

	if got := atomic.LoadInt32(creates); got != 1 {
		t.Fatalf("create calls = %d, want 1 (retry reused the cached file)", got)
	}
	if len(retryRefs) != 1 || retryRefs[0].FileID != refs[0].FileID {
		t.Fatalf("retry refs = %#v, want reuse of %#v", retryRefs, refs)
	}
}

func TestUploadTextContextFilesUploadsAgainForAnotherAccount(t *testing.T) {
	server, creates := countingUploadServer(t)
	defer server.Close()

	lookup := &staticAccountLookup{account: map[string]any{"email": "first@example.com"}}
	cache := NewTextAttachmentCache(time.Minute)

	first := &Client{BaseURL: server.URL, AccessToken: "token-a", httpClient: server.Client(), lookup: lookup}
	first.SetTextAttachmentCache(cache)
	if _, err := first.uploadTextContextFiles(t.Context(), []contextoffload.File{historyFile()}, ChatRequirements{}, time.Second); err != nil {
		t.Fatalf("first upload error = %v", err)
	}

	// The retry exhausted the first account and landed on another one: the file
	// id belongs to the previous account's library, so it must not be reused.
	lookup.set(map[string]any{"email": "second@example.com"})
	second := &Client{BaseURL: server.URL, AccessToken: "token-c", httpClient: server.Client(), lookup: lookup}
	second.SetTextAttachmentCache(cache)
	if _, err := second.uploadTextContextFiles(t.Context(), []contextoffload.File{historyFile()}, ChatRequirements{}, time.Second); err != nil {
		t.Fatalf("second upload error = %v", err)
	}

	if got := atomic.LoadInt32(creates); got != 2 {
		t.Fatalf("create calls = %d, want 2 (another account must upload its own file)", got)
	}
}

func TestUploadTextContextFilesUploadsAgainWhenContentChanges(t *testing.T) {
	server, creates := countingUploadServer(t)
	defer server.Close()

	lookup := &staticAccountLookup{account: map[string]any{"email": "same@example.com"}}
	cache := NewTextAttachmentCache(time.Minute)
	client := &Client{BaseURL: server.URL, AccessToken: "token-a", httpClient: server.Client(), lookup: lookup}
	client.SetTextAttachmentCache(cache)

	if _, err := client.uploadTextContextFiles(t.Context(), []contextoffload.File{historyFile()}, ChatRequirements{}, time.Second); err != nil {
		t.Fatalf("first upload error = %v", err)
	}
	changed := historyFile()
	changed.Text = "long history with one more turn"
	if _, err := client.uploadTextContextFiles(t.Context(), []contextoffload.File{changed}, ChatRequirements{}, time.Second); err != nil {
		t.Fatalf("changed upload error = %v", err)
	}

	if got := atomic.LoadInt32(creates); got != 2 {
		t.Fatalf("create calls = %d, want 2 (changed content is a different file)", got)
	}
}

func TestUploadTextContextFilesWithoutCacheAlwaysUploads(t *testing.T) {
	server, creates := countingUploadServer(t)
	defer server.Close()

	client := &Client{BaseURL: server.URL, AccessToken: "token-a", httpClient: server.Client()}
	for range 2 {
		if _, err := client.uploadTextContextFiles(t.Context(), []contextoffload.File{historyFile()}, ChatRequirements{}, time.Second); err != nil {
			t.Fatalf("upload error = %v", err)
		}
	}

	if got := atomic.LoadInt32(creates); got != 2 {
		t.Fatalf("create calls = %d, want 2 without a cache", got)
	}
}

func TestTextAttachmentCacheSkipsAccountWithoutEmail(t *testing.T) {
	server, creates := countingUploadServer(t)
	defer server.Close()

	lookup := &staticAccountLookup{account: map[string]any{"email": ""}}
	cache := NewTextAttachmentCache(time.Minute)
	client := &Client{BaseURL: server.URL, AccessToken: "token-a", httpClient: server.Client(), lookup: lookup}
	client.SetTextAttachmentCache(cache)

	for range 2 {
		if _, err := client.uploadTextContextFiles(t.Context(), []contextoffload.File{historyFile()}, ChatRequirements{}, time.Second); err != nil {
			t.Fatalf("upload error = %v", err)
		}
	}

	if got := atomic.LoadInt32(creates); got != 2 {
		t.Fatalf("create calls = %d, want 2 (no email means no safe cache identity)", got)
	}
}

func TestTextAttachmentCacheExpiresEntries(t *testing.T) {
	cache := NewTextAttachmentCache(10 * time.Millisecond)
	key := textAttachmentCacheKey("user@example.com", "history.txt", "content")
	cache.Put(key, TextAttachmentRef{FileID: "file_context", FileName: "history.txt"})

	if _, ok := cache.Get(key); !ok {
		t.Fatalf("Get() missed a fresh entry")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := cache.Get(key); ok {
		t.Fatalf("Get() returned an expired entry")
	}
}
