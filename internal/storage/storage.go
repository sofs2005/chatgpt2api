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
	// auto_vacuum 必须先于 journal_mode=WAL 设置：一旦切到 WAL，
	// 该 PRAGMA 就被锁定为当前值，之后再也改不动（实测设置静默失效）。
	//
	// 迁移失败不阻断启动：VACUUM 需要与原库相当的临时空间，磁盘紧张或存在
	// 并发实例时会失败，而此前 auto_vacuum=0 也能正常工作。VACUUM 是原子的，
	// 失败时库保持原状，下次启动会重试。
	_ = b.enableAutoVacuum()
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

const autoVacuumIncremental = 2

// enableAutoVacuum 打开增量 vacuum，使删除的行把空间归还给空闲列表并最终收缩文件。
//
// 没有它时，日志与图片元数据的保留期清理只是把页标记为空闲，文件永不缩小：
// 实测一个 232 MB 的库里有 227 MB 是已删除数据留下的空闲页，真实数据仅约 5 MB。
//
// 两个约束决定了这里的写法：
//   - auto_vacuum 只能在切到 WAL 之前、且 schema 为空时改变取值；
//   - 已有数据时必须 VACUUM 才能把新取值写进 header 并重排现有页。
//
// 因此已有库走「先 VACUUM 迁到 INCREMENTAL」这条一次性路径；迁移幂等，
// 已经是 INCREMENTAL 的库直接返回。
func (b *DatabaseBackend) enableAutoVacuum() error {
	var mode int
	if err := b.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return err
	}
	if mode == autoVacuumIncremental {
		return nil
	}
	// 1 = FULL, 2 = INCREMENTAL。取 INCREMENTAL：回收动作显式触发，
	// 不会在每次事务提交时搬页，写入路径的开销更可预期。
	if _, err := b.db.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		return err
	}
	var objects int
	if err := b.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','index')`,
	).Scan(&objects); err != nil {
		return err
	}
	if objects == 0 {
		// 空库：PRAGMA 已经生效，等第一次建表即可。
		return nil
	}
	// 已有库：VACUUM 才能把新设置写进 header 并重排现有页。
	// 这是唯一会重写整个文件的时刻，也顺带回收历史空闲页。
	if _, err := b.db.Exec(`VACUUM`); err != nil {
		return err
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
	deleted := int(rows)
	if deleted > 0 {
		// 删除只把页标记为空闲；不主动回收的话文件永不收缩。
		_ = b.reclaimFreePages()
	}
	return deleted, nil
}

// reclaimFreePages 把空闲页归还给操作系统。
//
// 这里用 VACUUM 而不是 PRAGMA incremental_vacuum：后者每次调用只回收一页，
// 面对保留期清理这种成批删除（实测 452 页空闲）需要调用数百次才能收敛，
// 而 VACUUM 一次就能把空闲列表清空（实测 452 → 0），并保持 auto_vacuum 设置不变。
//
// VACUUM 会重写整个文件，因此只在确有删除时触发——保留期清理每天至多一次，
// 开销可以接受。失败不影响删除结果，仅放弃本次收缩。
func (b *DatabaseBackend) reclaimFreePages() error {
	if b.driver != "sqlite" {
		return nil
	}
	_, err := b.db.Exec(`VACUUM`)
	return err
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
