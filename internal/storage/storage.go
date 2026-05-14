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
	QueryLogs(startDate, endDate string, limit int) ([]map[string]any, error)
}

type LogMaintenanceBackend interface {
	DeleteLogsBefore(day string) (int, error)
}

func NewBackendFromEnv(dataDir string) (Backend, error) {
	backendType := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_BACKEND")))
	if backendType == "" {
		backendType = "sqlite"
	}
	switch backendType {
	case "sqlite", "postgres", "postgresql", "mysql", "database":
		dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dsn == "" {
			dsn = "sqlite:///" + filepath.ToSlash(filepath.Join(dataDir, "chatgpt2api.db"))
		}
		return NewDatabaseBackend(dsn)
	default:
		return nil, fmt.Errorf("unknown storage backend: %s", backendType)
	}
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

func (b *DatabaseBackend) Close() error {
	if b == nil || b.db == nil {
		return nil
	}
	return b.db.Close()
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
		`CREATE INDEX IF NOT EXISTS idx_logs_day_id ON logs (day, id)`,
	}
	if b.driver == "postgres" {
		schema = []string{
			`CREATE TABLE IF NOT EXISTS accounts (id SERIAL PRIMARY KEY, access_token TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS auth_keys (id SERIAL PRIMARY KEY, key_id TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS json_documents (name TEXT PRIMARY KEY, data TEXT NOT NULL, updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS logs (id SERIAL PRIMARY KEY, created_at TEXT NOT NULL, type TEXT NOT NULL, day TEXT NOT NULL, data TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_logs_day_id ON logs (day, id)`,
		}
	}
	if b.driver == "mysql" {
		schema = []string{
			`CREATE TABLE IF NOT EXISTS accounts (id INTEGER PRIMARY KEY AUTO_INCREMENT, access_token TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS auth_keys (id INTEGER PRIMARY KEY AUTO_INCREMENT, key_id TEXT UNIQUE NOT NULL, data TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS json_documents (name VARCHAR(512) PRIMARY KEY, data LONGTEXT NOT NULL, updated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS logs (id INTEGER PRIMARY KEY AUTO_INCREMENT, created_at TEXT NOT NULL, type VARCHAR(64) NOT NULL, day VARCHAR(10) NOT NULL, data LONGTEXT NOT NULL)`,
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
	item["type"] = "event"
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	createdAt := strings.TrimSpace(fmt.Sprint(item["time"]))
	if createdAt == "" {
		createdAt = time.Now().Format("2006-01-02 15:04:05")
	}
	logType := "event"
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

func (b *DatabaseBackend) QueryLogs(startDate, endDate string, limit int) ([]map[string]any, error) {
	query := "SELECT data FROM logs"
	var filters []string
	var args []any
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

func (b *DatabaseBackend) DeleteLogsBefore(day string) (int, error) {
	day = strings.TrimSpace(day)
	if day == "" {
		return 0, nil
	}
	result, err := b.db.Exec("DELETE FROM logs WHERE day < "+b.placeholder(1), day)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(rows), nil
}

func (b *DatabaseBackend) placeholder(index int) string {
	if b.driver == "postgres" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
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

func logDay(value string) string {
	if len(value) < 10 {
		return ""
	}
	return value[:10]
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
