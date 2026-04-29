package service

import (
	"crypto/hmac"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

const (
	AuthRoleAdmin = "admin"
	AuthRoleUser  = "user"

	AuthKindAPIKey  = "api_key"
	AuthKindSession = "session"

	AuthProviderLocal   = "local"
	AuthProviderLinuxDo = "linuxdo"
)

type Identity struct {
	ID             string
	Name           string
	Role           string
	Provider       string
	OwnerID        string
	CredentialID   string
	CredentialName string
	Kind           string
}

func (i Identity) Map() map[string]any {
	return map[string]any{
		"id":              i.ID,
		"name":            i.Name,
		"role":            i.Role,
		"provider":        i.Provider,
		"owner_id":        i.OwnerID,
		"credential_id":   i.CredentialID,
		"credential_name": i.CredentialName,
		"kind":            i.Kind,
	}
}

type AuthOwner struct {
	ID       string
	Name     string
	Provider string
}

type AuthKeyFilter struct {
	Role    string
	Kind    string
	OwnerID string
}

type AuthService struct {
	mu              sync.Mutex
	storage         storage.Backend
	items           []map[string]any
	lastUsedFlushAt map[string]time.Time
}

func NewAuthService(backend storage.Backend) *AuthService {
	s := &AuthService{storage: backend, lastUsedFlushAt: map[string]time.Time{}}
	s.items = s.load()
	return s
}

func (s *AuthService) ListKeys(filter AuthKeyFilter) []map[string]any {
	filter = normalizeAuthKeyFilter(filter)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(s.items))
	for _, item := range s.items {
		if matchAuthKey(item, filter) {
			out = append(out, publicAuthItem(item))
		}
	}
	return out
}

func (s *AuthService) ListSingleAPIKeyForOwner(ownerID string) []map[string]any {
	ownerID = util.Clean(ownerID)
	if ownerID == "" {
		return []map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nextItems := s.items[:0]
	out := []map[string]any{}
	kept := false
	changed := false
	for _, item := range s.items {
		matchesOwnerAPIKey := util.Clean(item["role"]) == AuthRoleUser &&
			util.Clean(item["kind"]) == AuthKindAPIKey &&
			util.Clean(item["owner_id"]) == ownerID
		if !matchesOwnerAPIKey {
			nextItems = append(nextItems, item)
			continue
		}
		if kept {
			changed = true
			continue
		}
		kept = true
		nextItems = append(nextItems, item)
		out = append(out, publicAuthItem(item))
	}
	if changed {
		s.items = nextItems
		_ = s.saveLocked()
	}
	return out
}

func (s *AuthService) CreateAPIKey(role, name string, owner AuthOwner) (map[string]any, string, error) {
	return s.createCredential(role, AuthKindAPIKey, name, owner, "")
}

func (s *AuthService) UpsertAPIKeyForOwner(name string, owner AuthOwner) (map[string]any, string, error) {
	owner = normalizeAuthOwner(owner)
	if owner.ID == "" {
		return nil, "", errAuthOwnerRequired()
	}
	name = util.Clean(name)
	if name == "" {
		name = "我的 API 令牌"
	}
	raw := "sk-" + util.RandomTokenURL(24)
	now := util.NowISO()

	s.mu.Lock()
	defer s.mu.Unlock()
	nextItems := make([]map[string]any, 0, len(s.items)+1)
	var updated map[string]any
	for _, item := range s.items {
		matchesOwnerAPIKey := util.Clean(item["role"]) == AuthRoleUser &&
			util.Clean(item["kind"]) == AuthKindAPIKey &&
			util.Clean(item["owner_id"]) == owner.ID
		if !matchesOwnerAPIKey {
			nextItems = append(nextItems, item)
			continue
		}
		if updated != nil {
			continue
		}
		updated = util.CopyMap(item)
		updated["name"] = name
		updated["provider"] = owner.Provider
		updated["owner_name"] = owner.Name
		updated["key"] = raw
		updated["key_hash"] = util.SHA256Hex(raw)
		updated["enabled"] = true
		updated["last_used_at"] = nil
		updated["updated_at"] = now
		nextItems = append(nextItems, updated)
	}
	if updated == nil {
		updated = newAuthItem(AuthRoleUser, AuthKindAPIKey, name, owner, raw)
		nextItems = append(nextItems, updated)
	}
	s.items = nextItems
	if err := s.saveLocked(); err != nil {
		return nil, "", err
	}
	return publicAuthItem(updated), raw, nil
}

func (s *AuthService) UpsertLinuxDoSession(owner AuthOwner) (map[string]any, string, error) {
	owner.ID = util.Clean(owner.ID)
	owner.Name = util.Clean(owner.Name)
	owner.Provider = AuthProviderLinuxDo
	if owner.ID == "" {
		return nil, "", errAuthOwnerRequired()
	}
	name := owner.Name
	if name == "" {
		name = "Linuxdo 用户"
	}
	raw := "sess-" + util.RandomTokenURL(32)
	now := util.NowISO()

	s.mu.Lock()
	defer s.mu.Unlock()
	for index, item := range s.items {
		if util.Clean(item["kind"]) != AuthKindSession ||
			util.Clean(item["provider"]) != AuthProviderLinuxDo ||
			util.Clean(item["owner_id"]) != owner.ID {
			continue
		}
		next := util.CopyMap(item)
		next["name"] = name
		next["key"] = raw
		next["key_hash"] = util.SHA256Hex(raw)
		next["enabled"] = true
		next["owner_name"] = name
		next["last_used_at"] = nil
		next["updated_at"] = now
		s.items[index] = next
		if err := s.saveLocked(); err != nil {
			return nil, "", err
		}
		return publicAuthItem(next), raw, nil
	}

	item := newAuthItem(AuthRoleUser, AuthKindSession, name, owner, raw)
	s.items = append(s.items, item)
	if err := s.saveLocked(); err != nil {
		return nil, "", err
	}
	return publicAuthItem(item), raw, nil
}

func (s *AuthService) RevealKey(id string, filter AuthKeyFilter) (string, bool) {
	id = util.Clean(id)
	if id == "" {
		return "", false
	}
	filter = normalizeAuthKeyFilter(filter)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item["id"] != id || !matchAuthKey(item, filter) {
			continue
		}
		raw := util.Clean(item["key"])
		if raw == "" {
			return "", false
		}
		return raw, true
	}
	return "", false
}

func (s *AuthService) UpdateKey(id string, updates map[string]any, filter AuthKeyFilter) map[string]any {
	id = util.Clean(id)
	if id == "" {
		return nil
	}
	filter = normalizeAuthKeyFilter(filter)
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, item := range s.items {
		if item["id"] != id || !matchAuthKey(item, filter) {
			continue
		}
		next := util.CopyMap(item)
		if value, ok := updates["name"]; ok && value != nil {
			name := util.Clean(value)
			if name == "" {
				name = defaultCredentialName(util.Clean(next["role"]), util.Clean(next["kind"]))
			}
			next["name"] = name
		}
		if value, ok := updates["enabled"]; ok && value != nil {
			next["enabled"] = util.ToBool(value)
		}
		s.items[index] = next
		_ = s.saveLocked()
		return publicAuthItem(next)
	}
	return nil
}

func (s *AuthService) DeleteKey(id string, filter AuthKeyFilter) bool {
	id = util.Clean(id)
	if id == "" {
		return false
	}
	filter = normalizeAuthKeyFilter(filter)
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.items[:0]
	removed := false
	for _, item := range s.items {
		if item["id"] == id && matchAuthKey(item, filter) {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if !removed {
		return false
	}
	s.items = next
	_ = s.saveLocked()
	return true
}

func (s *AuthService) Authenticate(raw string) *Identity {
	candidate := util.Clean(raw)
	if candidate == "" {
		return nil
	}
	hash := util.SHA256Hex(candidate)
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, item := range s.items {
		if !util.ToBool(util.ValueOr(item["enabled"], true)) {
			continue
		}
		stored := util.Clean(item["key_hash"])
		if stored == "" || !hmac.Equal([]byte(stored), []byte(hash)) {
			continue
		}
		next := util.CopyMap(item)
		now := time.Now().UTC()
		next["last_used_at"] = now.Format(time.RFC3339Nano)
		s.items[index] = next
		id := util.Clean(next["id"])
		if last, ok := s.lastUsedFlushAt[id]; !ok || now.Sub(last) >= time.Minute {
			if s.saveLocked() == nil {
				s.lastUsedFlushAt[id] = now
			}
		}
		return identityForAuthItem(next)
	}
	return nil
}

func (s *AuthService) load() []map[string]any {
	items, err := s.storage.LoadAuthKeys()
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if normalized := normalizeAuthItem(item); normalized != nil {
			out = append(out, normalized)
		}
	}
	return out
}

func (s *AuthService) saveLocked() error {
	return s.storage.SaveAuthKeys(s.items)
}

func (s *AuthService) createCredential(role, kind, name string, owner AuthOwner, prefix string) (map[string]any, string, error) {
	role = normalizeAuthRole(role)
	if role == "" {
		role = AuthRoleUser
	}
	kind = normalizeAuthKind(kind)
	if kind == "" {
		kind = AuthKindAPIKey
	}
	owner = normalizeAuthOwner(owner)
	name = util.Clean(name)
	if name == "" {
		name = defaultCredentialName(role, kind)
	}
	if prefix == "" {
		prefix = "sk-"
	}
	raw := prefix + util.RandomTokenURL(24)
	item := newAuthItem(role, kind, name, owner, raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, item)
	if err := s.saveLocked(); err != nil {
		return nil, "", err
	}
	return publicAuthItem(item), raw, nil
}

func newAuthItem(role, kind, name string, owner AuthOwner, raw string) map[string]any {
	role = normalizeAuthRole(role)
	kind = normalizeAuthKind(kind)
	owner = normalizeAuthOwner(owner)
	name = util.Clean(name)
	if name == "" {
		name = defaultCredentialName(role, kind)
	}
	provider := owner.Provider
	if provider == "" {
		provider = AuthProviderLocal
	}
	return map[string]any{
		"id":           util.NewHex(12),
		"name":         name,
		"role":         role,
		"kind":         kind,
		"provider":     provider,
		"owner_id":     owner.ID,
		"owner_name":   owner.Name,
		"key":          raw,
		"key_hash":     util.SHA256Hex(raw),
		"enabled":      true,
		"created_at":   util.NowISO(),
		"last_used_at": nil,
	}
}

func normalizeAuthItem(raw map[string]any) map[string]any {
	role := normalizeAuthRole(util.Clean(raw["role"]))
	if role == "" {
		return nil
	}
	key := util.Clean(raw["key"])
	if key == "" {
		return nil
	}
	hash := util.Clean(raw["key_hash"])
	if hash == "" {
		return nil
	}
	if util.SHA256Hex(key) != hash {
		return nil
	}
	kind := normalizeAuthKind(util.Clean(raw["kind"]))
	if kind == "" {
		kind = AuthKindAPIKey
	}
	id := util.Clean(raw["id"])
	if id == "" {
		id = util.NewHex(12)
	}
	name := util.Clean(raw["name"])
	if name == "" {
		name = defaultCredentialName(role, kind)
	}
	owner := AuthOwner{
		ID:       util.Clean(raw["owner_id"]),
		Name:     util.Clean(raw["owner_name"]),
		Provider: normalizeAuthProvider(util.Clean(raw["provider"])),
	}
	if owner.Provider == "" {
		owner.Provider = AuthProviderLocal
	}
	created := util.Clean(raw["created_at"])
	if created == "" {
		created = util.NowISO()
	}
	lastUsed := raw["last_used_at"]
	if util.Clean(lastUsed) == "" {
		lastUsed = nil
	}
	out := map[string]any{
		"id":           id,
		"name":         name,
		"role":         role,
		"kind":         kind,
		"provider":     owner.Provider,
		"owner_id":     owner.ID,
		"owner_name":   owner.Name,
		"key":          key,
		"key_hash":     hash,
		"enabled":      util.ToBool(util.ValueOr(raw["enabled"], true)),
		"created_at":   created,
		"last_used_at": lastUsed,
	}
	if updated := util.Clean(raw["updated_at"]); updated != "" {
		out["updated_at"] = updated
	}
	return out
}

func publicAuthItem(item map[string]any) map[string]any {
	return map[string]any{
		"id":           item["id"],
		"name":         item["name"],
		"role":         item["role"],
		"kind":         item["kind"],
		"provider":     item["provider"],
		"owner_id":     item["owner_id"],
		"owner_name":   item["owner_name"],
		"enabled":      util.ToBool(util.ValueOr(item["enabled"], true)),
		"created_at":   item["created_at"],
		"last_used_at": item["last_used_at"],
	}
}

func identityForAuthItem(item map[string]any) *Identity {
	credentialID := util.Clean(item["id"])
	credentialName := util.Clean(item["name"])
	ownerID := util.Clean(item["owner_id"])
	ownerName := util.Clean(item["owner_name"])
	id := ownerID
	if id == "" {
		id = credentialID
	}
	name := ownerName
	if name == "" {
		name = credentialName
	}
	return &Identity{
		ID:             id,
		Name:           name,
		Role:           util.Clean(item["role"]),
		Provider:       util.Clean(item["provider"]),
		OwnerID:        ownerID,
		CredentialID:   credentialID,
		CredentialName: credentialName,
		Kind:           util.Clean(item["kind"]),
	}
}

func normalizeAuthKeyFilter(filter AuthKeyFilter) AuthKeyFilter {
	return AuthKeyFilter{
		Role:    normalizeAuthRole(util.Clean(filter.Role)),
		Kind:    normalizeAuthKind(util.Clean(filter.Kind)),
		OwnerID: util.Clean(filter.OwnerID),
	}
}

func matchAuthKey(item map[string]any, filter AuthKeyFilter) bool {
	if filter.Role != "" && util.Clean(item["role"]) != filter.Role {
		return false
	}
	if filter.Kind != "" && util.Clean(item["kind"]) != filter.Kind {
		return false
	}
	if filter.OwnerID != "" && util.Clean(item["owner_id"]) != filter.OwnerID {
		return false
	}
	return true
}

func normalizeAuthRole(role string) string {
	switch role {
	case AuthRoleAdmin, AuthRoleUser:
		return role
	default:
		return ""
	}
}

func normalizeAuthKind(kind string) string {
	switch kind {
	case "", AuthKindAPIKey:
		return AuthKindAPIKey
	case AuthKindSession:
		return AuthKindSession
	default:
		return ""
	}
}

func normalizeAuthProvider(provider string) string {
	switch provider {
	case "", AuthProviderLocal:
		return AuthProviderLocal
	case AuthProviderLinuxDo:
		return AuthProviderLinuxDo
	default:
		return provider
	}
}

func normalizeAuthOwner(owner AuthOwner) AuthOwner {
	owner.ID = util.Clean(owner.ID)
	owner.Name = util.Clean(owner.Name)
	owner.Provider = normalizeAuthProvider(util.Clean(owner.Provider))
	if owner.ID == "" {
		owner.Provider = AuthProviderLocal
	}
	return owner
}

func defaultCredentialName(role, kind string) string {
	if kind == AuthKindSession {
		return "登录会话"
	}
	if role == AuthRoleAdmin {
		return "管理员密钥"
	}
	return "普通用户"
}

func errAuthOwnerRequired() error {
	return authError("owner_id is required")
}

type authError string

func (e authError) Error() string {
	return string(e)
}
