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
