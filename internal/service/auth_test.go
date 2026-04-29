package service

import (
	"path/filepath"
	"testing"

	"chatgpt2api/internal/storage"
)

func TestAuthServiceCreateAuthenticateDisableAndDelete(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	filter := AuthKeyFilter{Role: AuthRoleUser, Kind: AuthKindAPIKey}
	public, raw, err := auth.CreateAPIKey(AuthRoleUser, "绘图用户", AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if raw == "" {
		t.Fatal("CreateAPIKey() returned empty raw key")
	}
	if _, ok := public["key_hash"]; ok {
		t.Fatalf("public key item leaked key_hash: %#v", public)
	}
	if _, ok := public["key"]; ok {
		t.Fatalf("public key item leaked raw key: %#v", public)
	}

	identity := auth.Authenticate(raw)
	if identity == nil {
		t.Fatal("Authenticate(raw) returned nil")
	}
	if identity.Role != "user" || identity.Name != "绘图用户" {
		t.Fatalf("identity = %#v", identity)
	}

	keyID, _ := public["id"].(string)
	revealed, found := auth.RevealKey(keyID, filter)
	if !found || revealed != raw {
		t.Fatalf("RevealKey() = %q, %v; want raw, true", revealed, found)
	}

	updated := auth.UpdateKey(keyID, map[string]any{"enabled": false}, filter)
	if updated == nil {
		t.Fatal("UpdateKey() returned nil")
	}
	if auth.Authenticate(raw) != nil {
		t.Fatal("disabled key still authenticated")
	}

	if !auth.DeleteKey(keyID, filter) {
		t.Fatal("DeleteKey() = false")
	}
	if len(auth.ListKeys(filter)) != 0 {
		t.Fatalf("ListKeys(user) after delete = %#v", auth.ListKeys(filter))
	}
}

func TestAuthServiceLinuxDoSessionOwnsAPIKeys(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	owner := AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: AuthProviderLinuxDo}
	_, rawSession, err := auth.UpsertLinuxDoSession(owner)
	if err != nil || rawSession == "" {
		t.Fatalf("UpsertLinuxDoSession() raw=%q err=%v", rawSession, err)
	}
	sessionIdentity := auth.Authenticate(rawSession)
	if sessionIdentity == nil {
		t.Fatal("Authenticate(session) returned nil")
	}
	if sessionIdentity.ID != owner.ID || sessionIdentity.OwnerID != owner.ID || sessionIdentity.Provider != AuthProviderLinuxDo || sessionIdentity.Kind != AuthKindSession {
		t.Fatalf("session identity = %#v", sessionIdentity)
	}

	item, rawAPIKey, err := auth.CreateAPIKey(AuthRoleUser, "绘图 API", owner)
	if err != nil {
		t.Fatalf("CreateAPIKey(owner) error = %v", err)
	}
	if rawAPIKey == "" {
		t.Fatal("CreateAPIKey(owner) returned empty key")
	}
	apiIdentity := auth.Authenticate(rawAPIKey)
	if apiIdentity == nil {
		t.Fatal("Authenticate(api key) returned nil")
	}
	if apiIdentity.ID != owner.ID || apiIdentity.CredentialID != item["id"] || apiIdentity.CredentialName != "绘图 API" {
		t.Fatalf("api identity = %#v", apiIdentity)
	}

	ownerFilter := AuthKeyFilter{Role: AuthRoleUser, Kind: AuthKindAPIKey, OwnerID: owner.ID}
	keys := auth.ListKeys(ownerFilter)
	if len(keys) != 1 || keys[0]["owner_id"] != owner.ID {
		t.Fatalf("owner scoped keys = %#v", keys)
	}
	allAPIKeys := auth.ListKeys(AuthKeyFilter{Role: AuthRoleUser, Kind: AuthKindAPIKey})
	if len(allAPIKeys) != 1 {
		t.Fatalf("all API keys should exclude sessions: %#v", allAPIKeys)
	}
}

func TestAuthServiceUpsertAPIKeyForOwnerKeepsOneToken(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	owner := AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: AuthProviderLinuxDo}
	if items := auth.ListSingleAPIKeyForOwner(owner.ID); len(items) != 0 {
		t.Fatalf("new owner should start with no token, got %#v", items)
	}

	first, firstRaw, err := auth.UpsertAPIKeyForOwner("", owner)
	if err != nil {
		t.Fatalf("first UpsertAPIKeyForOwner() error = %v", err)
	}
	second, secondRaw, err := auth.UpsertAPIKeyForOwner("", owner)
	if err != nil {
		t.Fatalf("second UpsertAPIKeyForOwner() error = %v", err)
	}
	if first["id"] != second["id"] {
		t.Fatalf("upsert should keep the same item id, first=%#v second=%#v", first, second)
	}
	if firstRaw == secondRaw {
		t.Fatal("upsert should rotate the raw token")
	}
	if auth.Authenticate(firstRaw) != nil {
		t.Fatal("old owner token still authenticated after reset")
	}
	if identity := auth.Authenticate(secondRaw); identity == nil || identity.ID != owner.ID {
		t.Fatalf("new owner token identity = %#v", identity)
	}
	keys := auth.ListKeys(AuthKeyFilter{Role: AuthRoleUser, Kind: AuthKindAPIKey, OwnerID: owner.ID})
	if len(keys) != 1 {
		t.Fatalf("owner should have exactly one token, got %#v", keys)
	}
}

func TestAuthServiceListSingleAPIKeyForOwnerPrunesDuplicates(t *testing.T) {
	backend := storage.NewJSONBackend(
		filepath.Join(t.TempDir(), "accounts.json"),
		filepath.Join(t.TempDir(), "auth_keys.json"),
	)
	auth := NewAuthService(backend)

	owner := AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: AuthProviderLinuxDo}
	first, firstRaw, err := auth.CreateAPIKey(AuthRoleUser, "first", owner)
	if err != nil {
		t.Fatalf("CreateAPIKey(first) error = %v", err)
	}
	_, secondRaw, err := auth.CreateAPIKey(AuthRoleUser, "second", owner)
	if err != nil {
		t.Fatalf("CreateAPIKey(second) error = %v", err)
	}
	items := auth.ListSingleAPIKeyForOwner(owner.ID)
	if len(items) != 1 || items[0]["id"] != first["id"] {
		t.Fatalf("ListSingleAPIKeyForOwner() = %#v, want first token only", items)
	}
	if auth.Authenticate(firstRaw) == nil {
		t.Fatal("kept token should still authenticate")
	}
	if auth.Authenticate(secondRaw) != nil {
		t.Fatal("pruned duplicate token still authenticated")
	}
}
