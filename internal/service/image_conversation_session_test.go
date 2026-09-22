package service

import (
	"path/filepath"
	"testing"
	"time"

	"chatgpt2api/internal/storage"
)

func newImageConversationSessionTestBackend(t *testing.T) storage.Backend {
	t.Helper()
	backend, err := storage.NewDatabaseBackend("sqlite:///" + filepath.ToSlash(filepath.Join(t.TempDir(), "test.db")))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}

func TestImageConversationSessionServiceScopesBindings(t *testing.T) {
	backend := newImageConversationSessionTestBackend(t)
	svc := NewImageConversationSessionService(filepath.Join(t.TempDir(), "image_conversation_sessions.json"), backend)

	if _, ok := svc.Get("owner-a", "frontend-1"); ok {
		t.Fatal("Get() found binding before Bind()")
	}

	first := ImageConversationSession{
		OwnerID:                 "owner-a",
		FrontendConversationID:  "frontend-1",
		AccessToken:             "token-a",
		UpstreamConversationID:  "conv-a",
		UpstreamParentMessageID: "msg-a",
	}
	svc.Bind(first)

	if _, ok := svc.Get("owner-b", "frontend-1"); ok {
		t.Fatal("Get() leaked binding across owners")
	}
	got, ok := svc.Get("owner-a", "frontend-1")
	if !ok {
		t.Fatal("Get() did not find owner binding")
	}
	if got.AccessToken != "token-a" || got.UpstreamConversationID != "conv-a" || got.UpstreamParentMessageID != "msg-a" || got.Status != ImageConversationSessionActive {
		t.Fatalf("binding = %#v", got)
	}
}

func TestImageConversationSessionServiceOverwriteInvalidateCleanupAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_conversation_sessions.json")
	backend := newImageConversationSessionTestBackend(t)
	svc := NewImageConversationSessionService(path, backend)
	svc.Bind(ImageConversationSession{OwnerID: "owner", FrontendConversationID: "front", AccessToken: "old", UpstreamConversationID: "conv-old", UpstreamParentMessageID: "msg-old"})
	svc.Bind(ImageConversationSession{OwnerID: "owner", FrontendConversationID: "front", AccessToken: "new", UpstreamConversationID: "conv-new", UpstreamParentMessageID: "msg-new"})

	got, ok := svc.Get("owner", "front")
	if !ok || got.AccessToken != "new" || got.UpstreamConversationID != "conv-new" || got.UpstreamParentMessageID != "msg-new" {
		t.Fatalf("overwritten binding = %#v ok=%v", got, ok)
	}

	reloaded := NewImageConversationSessionService(path, backend)
	reloadedGot, ok := reloaded.Get("owner", "front")
	if !ok || reloadedGot.AccessToken != "new" || reloadedGot.UpstreamConversationID != "conv-new" || reloadedGot.UpstreamParentMessageID != "msg-new" {
		t.Fatalf("reloaded binding = %#v ok=%v", reloadedGot, ok)
	}

	reloaded.Invalidate("owner", "front")
	invalid, ok := reloaded.Get("owner", "front")
	if !ok || invalid.Status != ImageConversationSessionFailed {
		t.Fatalf("invalidated binding = %#v ok=%v", invalid, ok)
	}

	old := time.Now().Add(-48 * time.Hour)
	reloaded.Bind(ImageConversationSession{OwnerID: "owner", FrontendConversationID: "old", AccessToken: "token", UpstreamConversationID: "conv", UpstreamParentMessageID: "msg", LastUsedAt: old})
	removed := reloaded.Cleanup(24 * time.Hour)
	if removed != 1 {
		t.Fatalf("Cleanup() removed %d, want 1", removed)
	}
	if _, ok := reloaded.Get("owner", "old"); ok {
		t.Fatal("Cleanup() kept expired binding")
	}
}

// Cleanup 必须能被过期项真正触发删除；这是会话表唯一的收缩路径。
// 该逻辑此前没有任何调用点，导致 image_conversation_sessions.json 只增不减。
func TestImageConversationSessionServiceCleanupRemovesExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image_conversation_sessions.json")
	backend := newImageConversationSessionTestBackend(t)
	svc := NewImageConversationSessionService(path, backend)

	old := time.Now().Add(-72 * time.Hour)
	fresh := time.Now().Add(-1 * time.Hour)
	svc.Bind(ImageConversationSession{OwnerID: "owner", FrontendConversationID: "old", AccessToken: "t1", UpstreamConversationID: "c1", UpstreamParentMessageID: "m1", LastUsedAt: old})
	svc.Bind(ImageConversationSession{OwnerID: "owner", FrontendConversationID: "fresh", AccessToken: "t2", UpstreamConversationID: "c2", UpstreamParentMessageID: "m2", LastUsedAt: fresh})

	if removed := svc.Cleanup(48 * time.Hour); removed != 1 {
		t.Fatalf("Cleanup() removed %d, want 1", removed)
	}
	if _, ok := svc.Get("owner", "old"); ok {
		t.Fatal("Cleanup() kept the expired binding")
	}
	if _, ok := svc.Get("owner", "fresh"); !ok {
		t.Fatal("Cleanup() removed the fresh binding")
	}
	// 过期项被持久化删除，重载后不应复活。
	reloaded := NewImageConversationSessionService(path, backend)
	if _, ok := reloaded.Get("owner", "old"); ok {
		t.Fatal("expired binding reappeared after reload")
	}
	if _, ok := reloaded.Get("owner", "fresh"); !ok {
		t.Fatal("fresh binding lost after reload")
	}
}
