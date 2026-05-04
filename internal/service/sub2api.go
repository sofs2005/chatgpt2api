package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

type Sub2APIConfig struct {
	mu      sync.Mutex
	path    string
	store   storage.JSONDocumentBackend
	servers []map[string]any
	docName string
}

type Sub2APIService struct {
	config   *Sub2APIConfig
	accounts *AccountService
	tokenMu  sync.Mutex
	cache    map[string]cachedJWT
}

type cachedJWT struct {
	token     string
	expiresAt time.Time
}

func NewSub2APIConfig(dataDir string, backend ...storage.Backend) *Sub2APIConfig {
	c := &Sub2APIConfig{path: filepath.Join(dataDir, "sub2api_config.json"), store: firstJSONDocumentStore(backend), docName: "sub2api_config.json"}
	c.servers = c.load()
	return c
}

func NewSub2APIService(config *Sub2APIConfig, accounts *AccountService) *Sub2APIService {
	return &Sub2APIService{config: config, accounts: accounts, cache: map[string]cachedJWT{}}
}

func (c *Sub2APIConfig) ListServers() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return copyMaps(c.servers)
}

func (c *Sub2APIConfig) GetServer(id string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, server := range c.servers {
		if server["id"] == id {
			return util.CopyMap(server)
		}
	}
	return nil
}

func (c *Sub2APIConfig) AddServer(name, baseURL, email, password, apiKey, groupID string) map[string]any {
	server := normalizeSub2Server(map[string]any{"id": util.NewHex(12), "name": name, "base_url": baseURL, "email": email, "password": password, "api_key": apiKey, "group_id": groupID})
	c.mu.Lock()
	c.servers = append(c.servers, server)
	_ = c.saveLocked()
	c.mu.Unlock()
	return util.CopyMap(server)
}

func (c *Sub2APIConfig) UpdateServer(id string, updates map[string]any) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, server := range c.servers {
		if server["id"] != id {
			continue
		}
		c.servers[index] = normalizeSub2Server(mergeMaps(server, updates, map[string]any{"id": id}))
		_ = c.saveLocked()
		return util.CopyMap(c.servers[index])
	}
	return nil
}

func (c *Sub2APIConfig) DeleteServer(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.servers[:0]
	removed := false
	for _, server := range c.servers {
		if server["id"] == id {
			removed = true
			continue
		}
		next = append(next, server)
	}
	if removed {
		c.servers = next
		_ = c.saveLocked()
	}
	return removed
}

func (c *Sub2APIConfig) SetImportJob(id string, job map[string]any) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, server := range c.servers {
		if server["id"] != id {
			continue
		}
		next := util.CopyMap(server)
		next["import_job"] = normalizeImportJob(job, false)
		c.servers[index] = next
		_ = c.saveLocked()
		return util.CopyMap(next)
	}
	return nil
}

func (c *Sub2APIConfig) GetImportJob(id string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, server := range c.servers {
		if server["id"] == id {
			if job, ok := server["import_job"].(map[string]any); ok {
				return util.CopyMap(job)
			}
		}
	}
	return nil
}

func (c *Sub2APIConfig) load() []map[string]any {
	raw := util.AsMapSlice(loadStoredJSON(c.store, c.docName, c.path))
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		out = append(out, normalizeSub2Server(item))
	}
	return out
}

func (c *Sub2APIConfig) saveLocked() error {
	return saveStoredJSON(c.store, c.docName, c.path, c.servers)
}

func (s *Sub2APIService) ListRemoteGroups(ctx context.Context, server map[string]any) ([]map[string]any, error) {
	baseURL := util.Clean(server["base_url"])
	if baseURL == "" {
		return []map[string]any{}, nil
	}
	headers, err := s.authHeaders(ctx, server)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	payload, err := s.getJSON(ctx, strings.TrimRight(baseURL, "/")+"/api/v1/admin/groups/all?platform=openai", headers)
	if err != nil {
		return nil, err
	}
	data, _ := extractPagedItems(payload)
	for _, raw := range data {
		group, ok := raw.(map[string]any)
		if !ok || group["id"] == nil {
			continue
		}
		items = append(items, map[string]any{"id": util.Clean(group["id"]), "name": util.Clean(group["name"]), "description": util.Clean(group["description"]), "platform": util.Clean(group["platform"]), "status": util.Clean(group["status"]), "account_count": util.ToInt(group["account_count"], 0), "active_account_count": util.ToInt(group["active_account_count"], 0)})
	}
	return items, nil
}

func (s *Sub2APIService) ListRemoteAccounts(ctx context.Context, server map[string]any) ([]map[string]any, error) {
	baseURL := util.Clean(server["base_url"])
	if baseURL == "" {
		return []map[string]any{}, nil
	}
	headers, err := s.authHeaders(ctx, server)
	if err != nil {
		return nil, err
	}
	groupID := util.Clean(server["group_id"])
	items := []map[string]any{}
	page := 1
	for {
		u := strings.TrimRight(baseURL, "/") + fmt.Sprintf("/api/v1/admin/accounts?platform=openai&type=oauth&page=%d&page_size=200", page)
		if groupID != "" {
			u += "&group=" + urlQuery(groupID)
		}
		payload, err := s.getJSON(ctx, u, headers)
		if err != nil {
			return nil, err
		}
		data, total := extractPagedItems(payload)
		if len(data) == 0 {
			break
		}
		for _, raw := range data {
			account, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			credentials := util.StringMap(account["credentials"])
			accessToken := extractAccessToken(credentials)
			if accessToken == "" {
				continue
			}
			id := util.Clean(account["id"])
			if id == "" {
				id = util.Clean(credentials["chatgpt_account_id"])
			}
			items = append(items, map[string]any{"id": id, "name": util.Clean(account["name"]), "email": firstNonEmpty(util.Clean(credentials["email"]), util.Clean(account["name"])), "plan_type": util.Clean(credentials["plan_type"]), "status": util.Clean(account["status"]), "expires_at": util.Clean(credentials["expires_at"]), "has_refresh_token": util.Clean(credentials["refresh_token"]) != ""})
		}
		if page*200 >= total || len(data) < 200 {
			break
		}
		page++
	}
	return items, nil
}

func (s *Sub2APIService) StartImport(server map[string]any, accountIDs []string) (map[string]any, error) {
	var ids []string
	for _, id := range accountIDs {
		if value := strings.TrimSpace(id); value != "" {
			ids = append(ids, value)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("account ids is required")
	}
	serverID := util.Clean(server["id"])
	job := newImportJob(len(ids))
	saved := s.config.SetImportJob(serverID, job)
	if saved == nil {
		return nil, fmt.Errorf("server not found")
	}
	go s.runImport(serverID, server, ids)
	return util.CopyMap(saved["import_job"].(map[string]any)), nil
}

func (s *Sub2APIService) runImport(serverID string, server map[string]any, ids []string) {
	s.updateJob(serverID, map[string]any{"status": "running"})
	type result struct{ token, id, err string }
	results := make(chan result, len(ids))
	workers := minInt(8, maxInt(1, len(ids)))
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				token, err := s.fetchAccessTokenForAccount(context.Background(), server, id)
				if err != nil {
					results <- result{id: id, err: err.Error()}
				} else {
					results <- result{id: id, token: token}
				}
			}
		}()
	}
	go func() {
		for _, id := range ids {
			jobs <- id
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	var tokens []string
	for res := range results {
		if res.token != "" {
			tokens = append(tokens, res.token)
		} else {
			s.appendJobError(serverID, res.id, res.err)
		}
		current := s.config.GetImportJob(serverID)
		s.updateJob(serverID, map[string]any{"completed": util.ToInt(current["completed"], 0) + 1, "failed": len(anyList(current["errors"]))})
	}
	if len(tokens) == 0 {
		current := s.config.GetImportJob(serverID)
		s.updateJob(serverID, map[string]any{"status": "failed", "completed": util.ToInt(current["total"], 0), "failed": len(anyList(current["errors"]))})
		return
	}
	add := s.accounts.AddAccounts(tokens)
	refresh := s.accounts.RefreshAccounts(context.Background(), tokens)
	current := s.config.GetImportJob(serverID)
	s.updateJob(serverID, map[string]any{"status": "completed", "completed": len(ids), "added": util.ToInt(add["added"], 0), "skipped": util.ToInt(add["skipped"], 0), "refreshed": util.ToInt(refresh["refreshed"], 0), "failed": len(anyList(current["errors"]))})
}

func (s *Sub2APIService) fetchAccessTokenForAccount(ctx context.Context, server map[string]any, accountID string) (string, error) {
	baseURL := util.Clean(server["base_url"])
	headers, err := s.authHeaders(ctx, server)
	if err != nil {
		return "", err
	}
	payload, err := s.getJSON(ctx, strings.TrimRight(baseURL, "/")+"/api/v1/admin/accounts/"+accountID, headers)
	if err != nil {
		return "", err
	}
	account := unwrapEnvelope(payload)
	accountMap, ok := account.(map[string]any)
	if !ok {
		accountMap = payload
	}
	token := extractAccessToken(util.StringMap(accountMap["credentials"]))
	if token == "" {
		return "", fmt.Errorf("missing access_token")
	}
	return token, nil
}

func (s *Sub2APIService) authHeaders(ctx context.Context, server map[string]any) (map[string]string, error) {
	if apiKey := util.Clean(server["api_key"]); apiKey != "" {
		return map[string]string{"x-api-key": apiKey, "Accept": "application/json"}, nil
	}
	email := util.Clean(server["email"])
	password := util.Clean(server["password"])
	if email == "" || password == "" {
		return nil, fmt.Errorf("sub2api server requires email+password or api_key")
	}
	serverID := util.Clean(server["id"])
	s.tokenMu.Lock()
	if cached, ok := s.cache[serverID]; ok && cached.expiresAt.After(time.Now()) {
		s.tokenMu.Unlock()
		return map[string]string{"Authorization": "Bearer " + cached.token, "Accept": "application/json"}, nil
	}
	s.tokenMu.Unlock()
	token, expiresAt, err := s.login(ctx, util.Clean(server["base_url"]), email, password)
	if err != nil {
		return nil, err
	}
	s.tokenMu.Lock()
	s.cache[serverID] = cachedJWT{token: token, expiresAt: expiresAt}
	s.tokenMu.Unlock()
	return map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"}, nil
}

func (s *Sub2APIService) login(ctx context.Context, baseURL, email, password string) (string, time.Time, error) {
	body, _ := json.Marshal(map[string]any{"email": email, "password": password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/v1/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("sub2api login failed: HTTP %d %s", resp.StatusCode, string(data[:minInt(len(data), 200)]))
	}
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return "", time.Time{}, fmt.Errorf("sub2api login payload is invalid")
	}
	bodyValue := unwrapEnvelope(payload)
	bodyMap, ok := bodyValue.(map[string]any)
	if !ok {
		return "", time.Time{}, fmt.Errorf("sub2api login payload is invalid")
	}
	token := util.Clean(bodyMap["access_token"])
	if token == "" {
		return "", time.Time{}, fmt.Errorf("sub2api login did not return access_token")
	}
	expiresIn := util.ToInt(bodyMap["expires_in"], 3600)
	if expiresIn < 60 {
		expiresIn = 60
	}
	return token, time.Now().Add(time.Duration(expiresIn-300) * time.Second), nil
}

func (s *Sub2APIService) getJSON(ctx context.Context, url string, headers map[string]string) (map[string]any, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sub2api request failed: HTTP %d %s", resp.StatusCode, string(data[:minInt(len(data), 200)]))
	}
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return nil, fmt.Errorf("invalid payload")
	}
	return payload, nil
}

func (s *Sub2APIService) updateJob(serverID string, updates map[string]any) {
	current := s.config.GetImportJob(serverID)
	if current == nil {
		return
	}
	for key, value := range updates {
		current[key] = value
	}
	current["updated_at"] = util.NowISO()
	s.config.SetImportJob(serverID, current)
}

func (s *Sub2APIService) appendJobError(serverID, name, message string) {
	current := s.config.GetImportJob(serverID)
	if current == nil {
		return
	}
	errors := anyList(current["errors"])
	errors = append(errors, map[string]any{"name": name, "error": message})
	s.updateJob(serverID, map[string]any{"errors": errors, "failed": len(errors)})
}

func normalizeSub2Server(raw map[string]any) map[string]any {
	return map[string]any{"id": firstNonEmpty(util.Clean(raw["id"]), util.NewHex(12)), "name": util.Clean(raw["name"]), "base_url": util.Clean(raw["base_url"]), "email": util.Clean(raw["email"]), "password": util.Clean(raw["password"]), "api_key": util.Clean(raw["api_key"]), "group_id": util.Clean(raw["group_id"]), "import_job": normalizeImportJob(raw["import_job"], true)}
}

func extractAccessToken(credentials map[string]any) string {
	for _, key := range []string{"access_token", "accessToken", "token"} {
		if value := util.Clean(credentials[key]); value != "" {
			return value
		}
	}
	return ""
}

func unwrapEnvelope(payload map[string]any) any {
	if _, hasData := payload["data"]; hasData {
		if _, hasCode := payload["code"]; hasCode {
			return payload["data"]
		}
	}
	return payload
}

func extractPagedItems(payload map[string]any) ([]any, int) {
	inner := unwrapEnvelope(payload)
	if list, ok := asArray(inner); ok {
		return list, len(list)
	}
	if obj, ok := inner.(map[string]any); ok {
		for _, key := range []string{"items", "data", "list"} {
			if list, ok := asArray(obj[key]); ok {
				return list, util.ToInt(obj["total"], len(list))
			}
		}
	}
	return []any{}, 0
}

func asArray(value any) ([]any, bool) {
	if list, ok := value.([]any); ok {
		return list, true
	}
	if list, ok := value.([]map[string]any); ok {
		out := make([]any, len(list))
		for index, item := range list {
			out[index] = item
		}
		return out, true
	}
	return nil, false
}
