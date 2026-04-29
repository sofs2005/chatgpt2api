package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

type Backend interface {
	LoadAccounts() ([]map[string]any, error)
	SaveAccounts([]map[string]any) error
	LoadAuthKeys() ([]map[string]any, error)
	SaveAuthKeys([]map[string]any) error
	HealthCheck() map[string]any
	Info() map[string]any
}

type JSONDocumentBackend interface {
	LoadJSONDocument(name string) (any, error)
	SaveJSONDocument(name string, value any) error
	DeleteJSONDocument(name string) error
}

type LogBackend interface {
	AppendLog(item map[string]any) error
	QueryLogs(logType, startDate, endDate string, limit int) ([]map[string]any, error)
}

func NewBackendFromEnv(dataDir string) (Backend, error) {
	backendType := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_BACKEND")))
	if backendType == "" {
		backendType = "sqlite"
	}
	switch backendType {
	case "json":
		return NewJSONBackend(filepath.Join(dataDir, "accounts.json"), filepath.Join(dataDir, "auth_keys.json")), nil
	case "sqlite", "postgres", "postgresql", "mysql", "database":
		dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dsn == "" {
			dsn = "sqlite:///" + filepath.ToSlash(filepath.Join(dataDir, "chatgpt2api.db"))
		}
		return NewDatabaseBackend(dsn)
	case "git":
		repoURL := strings.TrimSpace(os.Getenv("GIT_REPO_URL"))
		if repoURL == "" {
			return nil, fmt.Errorf("GIT_REPO_URL is required when using git storage backend")
		}
		return NewGitBackend(GitOptions{
			RepoURL:          repoURL,
			Token:            strings.TrimSpace(os.Getenv("GIT_TOKEN")),
			Branch:           envDefault("GIT_BRANCH", "main"),
			FilePath:         envDefault("GIT_FILE_PATH", "accounts.json"),
			AuthKeysFilePath: envDefault("GIT_AUTH_KEYS_FILE_PATH", "auth_keys.json"),
			CacheDir:         filepath.Join(dataDir, "git_cache"),
		}), nil
	default:
		return nil, fmt.Errorf("unknown storage backend: %s", backendType)
	}
}

func envDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

type JSONBackend struct {
	dataDir      string
	filePath     string
	authKeysPath string
}

func NewJSONBackend(filePath, authKeysPath string) *JSONBackend {
	_ = os.MkdirAll(filepath.Dir(filePath), 0o755)
	_ = os.MkdirAll(filepath.Dir(authKeysPath), 0o755)
	return &JSONBackend{dataDir: filepath.Dir(filePath), filePath: filePath, authKeysPath: authKeysPath}
}

func (b *JSONBackend) LoadAccounts() ([]map[string]any, error) {
	return loadJSONList(b.filePath), nil
}

func (b *JSONBackend) SaveAccounts(accounts []map[string]any) error {
	return saveJSONValue(b.filePath, accounts)
}

func (b *JSONBackend) LoadAuthKeys() ([]map[string]any, error) {
	raw := loadJSONValue(b.authKeysPath)
	if obj, ok := raw.(map[string]any); ok {
		raw = obj["items"]
	}
	return anyListToMaps(raw), nil
}

func (b *JSONBackend) SaveAuthKeys(keys []map[string]any) error {
	return saveJSONValue(b.authKeysPath, map[string]any{"items": keys})
}

func (b *JSONBackend) HealthCheck() map[string]any {
	if _, err := os.Stat(b.filePath); err != nil && !os.IsNotExist(err) {
		return map[string]any{"status": "unhealthy", "backend": "json", "error": err.Error()}
	}
	return map[string]any{
		"status":                "healthy",
		"backend":               "json",
		"file_exists":           exists(b.filePath),
		"file_path":             b.filePath,
		"auth_keys_file_exists": exists(b.authKeysPath),
		"auth_keys_file_path":   b.authKeysPath,
	}
}

func (b *JSONBackend) Info() map[string]any {
	return map[string]any{
		"type":                  "json",
		"description":           "本地 JSON 文件存储",
		"file_path":             b.filePath,
		"file_exists":           exists(b.filePath),
		"auth_keys_file_path":   b.authKeysPath,
		"auth_keys_file_exists": exists(b.authKeysPath),
	}
}

func (b *JSONBackend) LoadJSONDocument(name string) (any, error) {
	full, err := b.documentPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeJSONBytes(data)
}

func (b *JSONBackend) SaveJSONDocument(name string, value any) error {
	full, err := b.documentPath(name)
	if err != nil {
		return err
	}
	return saveJSONValue(full, value)
}

func (b *JSONBackend) DeleteJSONDocument(name string) error {
	full, err := b.documentPath(name)
	if err != nil {
		return err
	}
	removeErr := os.Remove(full)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	removeEmptyParentDirs(b.dataDir, filepath.Dir(full))
	return nil
}

func (b *JSONBackend) AppendLog(item map[string]any) error {
	full, err := b.documentPath("logs.jsonl")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(full, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

func (b *JSONBackend) QueryLogs(logType, startDate, endDate string, limit int) ([]map[string]any, error) {
	full, err := b.documentPath("logs.jsonl")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0)
	for i := len(lines) - 1; i >= 0; i-- {
		if limit > 0 && len(out) >= limit {
			break
		}
		item, ok := decodeLogLine(lines[i])
		if !ok || !matchLogFilter(item, logType, startDate, endDate) {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (b *JSONBackend) documentPath(name string) (string, error) {
	rel, err := cleanDocumentName(name)
	if err != nil {
		return "", err
	}
	full := filepath.Join(b.dataDir, filepath.FromSlash(rel))
	root, err := filepath.Abs(b.dataDir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if resolved != root {
		relToRoot, err := filepath.Rel(root, resolved)
		if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relToRoot) {
			return "", fmt.Errorf("invalid document name: %s", name)
		}
	}
	return full, nil
}

type DatabaseBackend struct {
	databaseURL string
	driver      string
	dsn         string
	db          *sql.DB
}

func NewDatabaseBackend(databaseURL string) (*DatabaseBackend, error) {
	driver, dsn, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	backend := &DatabaseBackend{databaseURL: databaseURL, driver: driver, dsn: dsn, db: db}
	backend.configurePool()
	if err := backend.configureSQLite(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := backend.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return backend, nil
}

func (b *DatabaseBackend) configurePool() {
	b.db.SetConnMaxLifetime(time.Hour)
	if b.driver == "sqlite" {
		b.db.SetMaxOpenConns(1)
		b.db.SetMaxIdleConns(1)
		return
	}
	b.db.SetMaxOpenConns(10)
	b.db.SetMaxIdleConns(5)
}

func (b *DatabaseBackend) configureSQLite() error {
	if b.driver != "sqlite" {
		return nil
	}
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA temp_store=MEMORY`,
		`PRAGMA foreign_keys=ON`,
	} {
		if _, err := b.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (b *DatabaseBackend) init() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, access_token TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS auth_keys (id INTEGER PRIMARY KEY AUTOINCREMENT, key_id TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS json_documents (name TEXT PRIMARY KEY, data TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS logs (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, type TEXT NOT NULL, day TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_type_day_id ON logs (type, day, id)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_day_id ON logs (day, id)`,
	}
	if b.driver == "postgres" {
		schema = []string{
			`CREATE TABLE IF NOT EXISTS accounts (id SERIAL PRIMARY KEY, access_token TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS auth_keys (id SERIAL PRIMARY KEY, key_id TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS json_documents (name TEXT PRIMARY KEY, data TEXT NOT NULL, updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS logs (id SERIAL PRIMARY KEY, created_at TEXT NOT NULL, type TEXT NOT NULL, day TEXT NOT NULL, data TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_logs_type_day_id ON logs (type, day, id)`,
			`CREATE INDEX IF NOT EXISTS idx_logs_day_id ON logs (day, id)`,
		}
	}
	if b.driver == "mysql" {
		schema = []string{
			`CREATE TABLE IF NOT EXISTS accounts (id INTEGER PRIMARY KEY AUTO_INCREMENT, access_token TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS auth_keys (id INTEGER PRIMARY KEY AUTO_INCREMENT, key_id TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS json_documents (name VARCHAR(512) PRIMARY KEY, data LONGTEXT NOT NULL, updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS logs (id INTEGER PRIMARY KEY AUTO_INCREMENT, created_at TEXT NOT NULL, type VARCHAR(64) NOT NULL, day VARCHAR(10) NOT NULL, data LONGTEXT NOT NULL)`,
			`CREATE INDEX idx_logs_type_day_id ON logs (type, day, id)`,
			`CREATE INDEX idx_logs_day_id ON logs (day, id)`,
		}
	}
	for _, stmt := range schema {
		if _, err := b.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (b *DatabaseBackend) LoadAccounts() ([]map[string]any, error) {
	return b.loadRows("accounts")
}

func (b *DatabaseBackend) SaveAccounts(accounts []map[string]any) error {
	return b.saveRows("accounts", "access_token", accounts)
}

func (b *DatabaseBackend) LoadAuthKeys() ([]map[string]any, error) {
	return b.loadRows("auth_keys")
}

func (b *DatabaseBackend) SaveAuthKeys(keys []map[string]any) error {
	return b.saveRows("auth_keys", "key_id", keys)
}

func (b *DatabaseBackend) HealthCheck() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.db.PingContext(ctx); err != nil {
		return map[string]any{"status": "unhealthy", "backend": "database", "error": err.Error()}
	}
	accountCount := b.count("accounts")
	authKeyCount := b.count("auth_keys")
	documentCount := b.count("json_documents")
	logCount := b.count("logs")
	return map[string]any{"status": "healthy", "backend": "database", "database_url": maskPassword(b.databaseURL), "account_count": accountCount, "auth_key_count": authKeyCount, "document_count": documentCount, "log_count": logCount}
}

func (b *DatabaseBackend) Info() map[string]any {
	dbType := "unknown"
	switch b.driver {
	case "sqlite":
		dbType = "sqlite"
	case "postgres":
		dbType = "postgresql"
	case "mysql":
		dbType = "mysql"
	}
	return map[string]any{"type": "database", "db_type": dbType, "description": "数据库存储 (" + dbType + ")", "database_url": maskPassword(b.databaseURL)}
}

func (b *DatabaseBackend) loadRows(table string) ([]map[string]any, error) {
	rows, err := b.db.Query("SELECT data FROM " + table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			continue
		}
		var item map[string]any
		if json.Unmarshal([]byte(text), &item) == nil && item != nil {
			out = append(out, item)
		}
	}
	return out, rows.Err()
}

func (b *DatabaseBackend) saveRows(table, keyColumn string, items []map[string]any) error {
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.Exec("DELETE FROM " + table); err != nil {
		return err
	}
	sourceKey := "access_token"
	if table == "auth_keys" {
		sourceKey = "id"
	}
	stmtText := "INSERT INTO " + table + " (" + keyColumn + ", data) VALUES (?, ?)"
	if b.driver == "postgres" {
		stmtText = "INSERT INTO " + table + " (" + keyColumn + ", data) VALUES ($1, $2)"
	}
	stmt, err := tx.Prepare(stmtText)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range items {
		key := strings.TrimSpace(fmt.Sprint(item[sourceKey]))
		if key == "" {
			continue
		}
		data, err := json.Marshal(item)
		if err != nil {
			continue
		}
		if _, err := stmt.Exec(key, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (b *DatabaseBackend) count(table string) int {
	var count int
	_ = b.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
	return count
}

func (b *DatabaseBackend) LoadJSONDocument(name string) (any, error) {
	rel, err := cleanDocumentName(name)
	if err != nil {
		return nil, err
	}
	var text string
	err = b.db.QueryRow("SELECT data FROM json_documents WHERE name = "+b.placeholder(1), rel).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeJSONString(text)
}

func (b *DatabaseBackend) SaveJSONDocument(name string, value any) error {
	rel, err := cleanDocumentName(name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var stmt string
	switch b.driver {
	case "postgres":
		stmt = "INSERT INTO json_documents (name, data, updated_at) VALUES ($1, $2, $3) ON CONFLICT (name) DO UPDATE SET data = EXCLUDED.data, updated_at = EXCLUDED.updated_at"
	case "mysql":
		stmt = "REPLACE INTO json_documents (name, data, updated_at) VALUES (?, ?, ?)"
	default:
		stmt = "INSERT INTO json_documents (name, data, updated_at) VALUES (?, ?, ?) ON CONFLICT(name) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at"
	}
	_, err = b.db.Exec(stmt, rel, string(data), now)
	return err
}

func (b *DatabaseBackend) DeleteJSONDocument(name string) error {
	rel, err := cleanDocumentName(name)
	if err != nil {
		return err
	}
	_, err = b.db.Exec("DELETE FROM json_documents WHERE name = "+b.placeholder(1), rel)
	return err
}

func (b *DatabaseBackend) AppendLog(item map[string]any) error {
	if item == nil {
		item = map[string]any{}
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	createdAt := strings.TrimSpace(fmt.Sprint(item["time"]))
	if createdAt == "" {
		createdAt = time.Now().Format("2006-01-02 15:04:05")
	}
	logType := strings.TrimSpace(fmt.Sprint(item["type"]))
	day := logDay(createdAt)
	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	_, err = b.db.Exec(
		"INSERT INTO logs (created_at, type, day, data) VALUES ("+b.placeholder(1)+", "+b.placeholder(2)+", "+b.placeholder(3)+", "+b.placeholder(4)+")",
		createdAt,
		logType,
		day,
		string(data),
	)
	return err
}

func (b *DatabaseBackend) QueryLogs(logType, startDate, endDate string, limit int) ([]map[string]any, error) {
	query := "SELECT data FROM logs"
	var filters []string
	var args []any
	if strings.TrimSpace(logType) != "" {
		args = append(args, strings.TrimSpace(logType))
		filters = append(filters, "type = "+b.placeholder(len(args)))
	}
	if strings.TrimSpace(startDate) != "" {
		args = append(args, strings.TrimSpace(startDate))
		filters = append(filters, "day >= "+b.placeholder(len(args)))
	}
	if strings.TrimSpace(endDate) != "" {
		args = append(args, strings.TrimSpace(endDate))
		filters = append(filters, "day <= "+b.placeholder(len(args)))
	}
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}
	query += " ORDER BY id DESC"
	if limit > 0 {
		args = append(args, limit)
		query += " LIMIT " + b.placeholder(len(args))
	}
	rows, err := b.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			continue
		}
		item, err := decodeJSONString(text)
		if err != nil {
			continue
		}
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

func (b *DatabaseBackend) placeholder(index int) string {
	if b.driver == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

type GitOptions struct {
	RepoURL          string
	Token            string
	Branch           string
	FilePath         string
	AuthKeysFilePath string
	CacheDir         string
}

type GitBackend struct {
	options GitOptions
}

func NewGitBackend(options GitOptions) *GitBackend {
	if options.Branch == "" {
		options.Branch = "main"
	}
	if options.FilePath == "" {
		options.FilePath = "accounts.json"
	}
	if options.AuthKeysFilePath == "" {
		options.AuthKeysFilePath = "auth_keys.json"
	}
	_ = os.MkdirAll(options.CacheDir, 0o755)
	return &GitBackend{options: options}
}

func (b *GitBackend) LoadAccounts() ([]map[string]any, error) {
	data, err := b.loadValue(b.options.FilePath)
	if err != nil {
		return nil, err
	}
	return anyListToMaps(data), nil
}

func (b *GitBackend) SaveAccounts(accounts []map[string]any) error {
	return b.saveValue(b.options.FilePath, accounts, "Update accounts data")
}

func (b *GitBackend) LoadAuthKeys() ([]map[string]any, error) {
	data, err := b.loadValue(b.options.AuthKeysFilePath)
	if err != nil {
		return nil, err
	}
	if obj, ok := data.(map[string]any); ok {
		data = obj["items"]
	}
	return anyListToMaps(data), nil
}

func (b *GitBackend) SaveAuthKeys(keys []map[string]any) error {
	return b.saveValue(b.options.AuthKeysFilePath, map[string]any{"items": keys}, "Update auth keys data")
}

func (b *GitBackend) HealthCheck() map[string]any {
	repo, err := b.cloneOrPull()
	if err != nil {
		return map[string]any{"status": "unhealthy", "backend": "git", "error": err.Error()}
	}
	commit, _ := gitOutput(repo, "rev-parse", "--short=8", "HEAD")
	return map[string]any{"status": "healthy", "backend": "git", "repo_url": maskToken(b.options.RepoURL), "branch": b.options.Branch, "file_path": b.options.FilePath, "auth_keys_file_path": b.options.AuthKeysFilePath, "last_commit": strings.TrimSpace(commit)}
}

func (b *GitBackend) Info() map[string]any {
	return map[string]any{"type": "git", "description": "Git 私有仓库存储", "repo_url": maskToken(b.options.RepoURL), "branch": b.options.Branch, "file_path": b.options.FilePath, "auth_keys_file_path": b.options.AuthKeysFilePath}
}

func (b *GitBackend) loadValue(filePath string) (any, error) {
	repo, err := b.cloneOrPull()
	if err != nil {
		return nil, err
	}
	full := filepath.Join(repo, filepath.FromSlash(filePath))
	data, err := os.ReadFile(full)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *GitBackend) saveValue(filePath string, value any, message string) error {
	repo, err := b.cloneOrPull()
	if err != nil {
		return err
	}
	full := filepath.Join(repo, filepath.FromSlash(filePath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := saveJSONValue(full, value); err != nil {
		return err
	}
	if _, err := gitOutput(repo, "add", filePath); err != nil {
		return err
	}
	status, err := gitOutput(repo, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	if _, err := gitOutput(repo, "commit", "-m", message); err != nil {
		return err
	}
	_, err = gitOutput(repo, "push", "origin", b.options.Branch)
	return err
}

func (b *GitBackend) cloneOrPull() (string, error) {
	repoPath := filepath.Join(b.options.CacheDir, "repo")
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
		if _, err := gitOutput(repoPath, "pull", "origin", b.options.Branch); err == nil {
			return repoPath, nil
		}
		_ = os.RemoveAll(repoPath)
	}
	authURL := buildAuthURL(b.options.RepoURL, b.options.Token)
	if _, err := gitOutput("", "clone", "--branch", b.options.Branch, authURL, repoPath); err != nil {
		return "", err
	}
	return repoPath, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func loadJSONList(path string) []map[string]any {
	return anyListToMaps(loadJSONValue(path))
}

func loadJSONValue(path string) any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out, err := decodeJSONBytes(data)
	if err != nil {
		return nil
	}
	return out
}

func saveJSONValue(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func anyListToMaps(raw any) []map[string]any {
	items, ok := raw.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cleanDocumentName(name string) (string, error) {
	raw := strings.TrimSpace(filepath.ToSlash(name))
	rel := path.Clean(raw)
	if raw != rel || rel == "." || rel == "" || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") || strings.ContainsRune(rel, 0) || filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("invalid document name: %s", name)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", fmt.Errorf("invalid document name: %s", name)
		}
	}
	return rel, nil
}

func decodeJSONString(text string) (any, error) {
	return decodeJSONBytes([]byte(text))
}

func decodeJSONBytes(data []byte) (any, error) {
	var out any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("invalid trailing JSON data")
	}
	return out, nil
}

func decodeLogLine(line string) (map[string]any, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, false
	}
	raw, err := decodeJSONString(line)
	if err != nil {
		return nil, false
	}
	item, ok := raw.(map[string]any)
	return item, ok
}

func matchLogFilter(item map[string]any, logType, startDate, endDate string) bool {
	day := logDay(strings.TrimSpace(fmt.Sprint(item["time"])))
	if strings.TrimSpace(logType) != "" && strings.TrimSpace(fmt.Sprint(item["type"])) != strings.TrimSpace(logType) {
		return false
	}
	if strings.TrimSpace(startDate) != "" && day < strings.TrimSpace(startDate) {
		return false
	}
	if strings.TrimSpace(endDate) != "" && day > strings.TrimSpace(endDate) {
		return false
	}
	return true
}

func logDay(value string) string {
	if len(value) < 10 {
		return ""
	}
	return value[:10]
}

func removeEmptyParentDirs(root, start string) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return
	}
	for current != rootAbs {
		rel, err := filepath.Rel(rootAbs, current)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return
		}
		if err := os.Remove(current); err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
		current = filepath.Dir(current)
	}
}

func parseDatabaseURL(databaseURL string) (driver, dsn string, err error) {
	lower := strings.ToLower(databaseURL)
	switch {
	case strings.HasPrefix(lower, "sqlite:///"):
		return "sqlite", strings.TrimPrefix(databaseURL, "sqlite:///"), nil
	case strings.HasPrefix(lower, "sqlite://"):
		return "sqlite", strings.TrimPrefix(databaseURL, "sqlite://"), nil
	case strings.HasPrefix(lower, "postgresql://"), strings.HasPrefix(lower, "postgres://"):
		return "postgres", databaseURL, nil
	case strings.HasPrefix(lower, "mysql://"):
		u, parseErr := url.Parse(databaseURL)
		if parseErr != nil {
			return "", "", parseErr
		}
		pass, _ := u.User.Password()
		user := u.User.Username()
		db := strings.TrimPrefix(u.Path, "/")
		return "mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true", user, pass, u.Host, db), nil
	default:
		if strings.Contains(lower, "postgres") {
			return "postgres", databaseURL, nil
		}
		return "sqlite", databaseURL, nil
	}
}

func maskPassword(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	username := u.User.Username()
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(username, "****")
	}
	return u.String()
}

func maskToken(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("****")
	return u.String()
}

func buildAuthURL(repoURL, token string) string {
	if token == "" {
		return repoURL
	}
	if strings.HasPrefix(repoURL, "https://") {
		return strings.Replace(repoURL, "https://", "https://"+url.QueryEscape(token)+"@", 1)
	}
	if strings.HasPrefix(repoURL, "git@") {
		converted := strings.Replace(repoURL, "git@", "https://", 1)
		converted = strings.Replace(converted, ".com:", ".com/", 1)
		return strings.Replace(converted, "https://", "https://"+url.QueryEscape(token)+"@", 1)
	}
	return repoURL
}
