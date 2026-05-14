package service

import (
	"path/filepath"
	"testing"

	"chatgpt2api/internal/storage"
)

func newTestStorageBackend(t *testing.T) storage.Backend {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "chatgpt2api.db")
	backend, err := storage.NewDatabaseBackend("sqlite:///" + filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	t.Cleanup(func() {
		_ = backend.Close()
	})
	return backend
}
