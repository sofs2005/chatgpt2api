package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreUpdatePersistsRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_BASE_URL")
	unsetEnv(t, "CHATGPT2API_PROXY")
	unsetEnv(t, "CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE")
	unsetEnv(t, "CHATGPT2API_IMAGE_CONCURRENT_LIMIT")
	unsetEnv(t, "CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT")
	unsetEnv(t, "CHATGPT2API_USER_DEFAULT_RPM_LIMIT")
	unsetEnv(t, "CHATGPT2API_IMAGE_RETENTION_DAYS")
	unsetEnv(t, "CHATGPT2API_LOG_RETENTION_DAYS")
	unsetEnv(t, "CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS")
	unsetEnv(t, "CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS")
	unsetEnv(t, "CHATGPT2API_REGISTRATION_ENABLED")
	unsetEnv(t, "CHATGPT2API_LOG_LEVELS")
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	got, err := store.Update(map[string]any{
		"base_url":                        "https://example.test/root/",
		"proxy":                           "http://127.0.0.1:8080",
		"refresh_account_interval_minute": 7,
		"image_concurrent_limit":          3,
		"user_default_concurrent_limit":   2,
		"user_default_rpm_limit":          30,
		"image_retention_days":            14,
		"log_retention_days":              21,
		"registration_enabled":            true,
		"log_levels":                      []any{"debug", "error"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if store.BaseURL() != "https://example.test/root" {
		t.Fatalf("BaseURL() = %q", store.BaseURL())
	}
	assertConfigValue(t, got, "registration_enabled", true)

	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	envText := string(envData)
	for _, want := range []string{
		"CHATGPT2API_BASE_URL=https://example.test/root/",
		"CHATGPT2API_PROXY=http://127.0.0.1:8080",
		"CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE=7",
		"CHATGPT2API_IMAGE_CONCURRENT_LIMIT=3",
		"CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT=2",
		"CHATGPT2API_USER_DEFAULT_RPM_LIMIT=30",
		"CHATGPT2API_IMAGE_RETENTION_DAYS=14",
		"CHATGPT2API_LOG_RETENTION_DAYS=21",
		"CHATGPT2API_REGISTRATION_ENABLED=true",
		"CHATGPT2API_LOG_LEVELS=debug,error",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf(".env missing %q in:\n%s", want, envText)
		}
	}
}

func TestStoreNormalizesUnsupportedLoginPageImageMode(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_LOGIN_PAGE_IMAGE_MODE")
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	got, err := store.Update(map[string]any{"login_page_image_mode": "repeat"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertConfigValue(t, got, "login_page_image_mode", "contain")
	if store.LoginPageImageMode() != "contain" {
		t.Fatalf("LoginPageImageMode() = %q, want contain", store.LoginPageImageMode())
	}
	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	envText := string(envData)
	if strings.Contains(envText, "CHATGPT2API_LOGIN_PAGE_IMAGE_MODE=repeat") {
		t.Fatalf(".env persisted unsupported login page image mode:\n%s", envText)
	}
	if !strings.Contains(envText, "CHATGPT2API_LOGIN_PAGE_IMAGE_MODE=contain") {
		t.Fatalf(".env missing normalized login page image mode:\n%s", envText)
	}
}

func TestStoreUpdatePersistsLinuxDoSettingsWithoutLeakingSecret(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	got, err := store.Update(map[string]any{
		"linuxdo_enabled":               true,
		"linuxdo_client_id":             "client-id",
		"linuxdo_client_secret":         "client-secret",
		"linuxdo_redirect_url":          "https://example.test/auth/linuxdo/oauth/callback",
		"linuxdo_frontend_redirect_url": "http://127.0.0.1:5173/auth/linuxdo/callback",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	assertConfigValue(t, got, "linuxdo_enabled", true)
	assertConfigValue(t, got, "linuxdo_client_id", "client-id")
	assertConfigValue(t, got, "linuxdo_client_secret_configured", true)
	assertConfigValue(t, got, "linuxdo_redirect_url", "https://example.test/auth/linuxdo/oauth/callback")
	assertConfigValue(t, got, "linuxdo_frontend_redirect_url", "http://127.0.0.1:5173/auth/linuxdo/callback")
	if _, ok := got["linuxdo_client_secret"]; ok {
		t.Fatalf("Get() leaked linuxdo_client_secret: %#v", got)
	}
	if !store.LinuxDoOAuth().Ready() {
		t.Fatalf("LinuxDoOAuth() should be ready: %#v", store.LinuxDoOAuth())
	}

	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	envText := string(envData)
	for _, want := range []string{
		"CHATGPT2API_LINUXDO_ENABLED=true",
		"CHATGPT2API_LINUXDO_CLIENT_ID=client-id",
		"CHATGPT2API_LINUXDO_CLIENT_SECRET=client-secret",
		"CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL=http://127.0.0.1:5173/auth/linuxdo/callback",
		"CHATGPT2API_LINUXDO_REDIRECT_URL=https://example.test/auth/linuxdo/oauth/callback",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf(".env missing %q in:\n%s", want, envText)
		}
	}

	got, err = store.Update(map[string]any{
		"linuxdo_enabled":               true,
		"linuxdo_client_id":             "client-id-next",
		"linuxdo_client_secret":         "",
		"linuxdo_redirect_url":          "https://example.test/auth/linuxdo/oauth/callback",
		"linuxdo_frontend_redirect_url": "/auth/linuxdo/callback",
	})
	if err != nil {
		t.Fatalf("Update() with blank secret error = %v", err)
	}
	assertConfigValue(t, got, "linuxdo_client_id", "client-id-next")
	assertConfigValue(t, got, "linuxdo_client_secret_configured", true)
	assertConfigValue(t, got, "linuxdo_frontend_redirect_url", "/auth/linuxdo/callback")
	if store.LinuxDoOAuth().ClientSecret != "client-secret" {
		t.Fatalf("blank secret update should preserve existing secret")
	}
}

func TestStoreUpdateRejectsIncompleteLinuxDoSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	_, err = store.Update(map[string]any{
		"linuxdo_enabled":      true,
		"linuxdo_client_id":    "client-id",
		"linuxdo_redirect_url": "https://example.test/auth/linuxdo/oauth/callback",
	})
	if err == nil || !strings.Contains(err.Error(), "Client Secret") {
		t.Fatalf("Update() error = %v, want missing secret", err)
	}
}

func TestStoreUpdateRefreshesEnvFileBackedRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	envText := strings.Join([]string{
		"CHATGPT2API_BASE_URL=https://old.example/root",
		"CHATGPT2API_PROXY=http://127.0.0.1:8080",
		"CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE=5",
		"CHATGPT2API_IMAGE_CONCURRENT_LIMIT=4",
		"CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT=2",
		"CHATGPT2API_USER_DEFAULT_RPM_LIMIT=30",
		"CHATGPT2API_IMAGE_RETENTION_DAYS=30",
		"CHATGPT2API_LOG_RETENTION_DAYS=7",
		"CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS=true",
		"CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS=false",
		"CHATGPT2API_LOG_LEVELS=warning,error",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(envText), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("CHATGPT2API_ROOT", root)
	t.Setenv("CHATGPT2API_BASE_URL", "https://old.example/root")
	t.Setenv("CHATGPT2API_PROXY", "http://127.0.0.1:8080")
	t.Setenv("CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE", "5")
	t.Setenv("CHATGPT2API_IMAGE_CONCURRENT_LIMIT", "4")
	t.Setenv("CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT", "2")
	t.Setenv("CHATGPT2API_USER_DEFAULT_RPM_LIMIT", "30")
	t.Setenv("CHATGPT2API_IMAGE_RETENTION_DAYS", "30")
	t.Setenv("CHATGPT2API_LOG_RETENTION_DAYS", "7")
	t.Setenv("CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS", "true")
	t.Setenv("CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS", "false")
	t.Setenv("CHATGPT2API_LOG_LEVELS", "warning,error")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	got, err := store.Update(map[string]any{
		"base_url":                          "https://new.example/root/",
		"proxy":                             "http://127.0.0.1:9090",
		"refresh_account_interval_minute":   9,
		"image_concurrent_limit":            6,
		"user_default_concurrent_limit":     3,
		"user_default_rpm_limit":            45,
		"image_retention_days":              12,
		"log_retention_days":                30,
		"auto_remove_invalid_accounts":      false,
		"auto_remove_rate_limited_accounts": true,
		"log_levels":                        []any{"debug", "info"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	assertConfigValue(t, got, "base_url", "https://new.example/root")
	assertConfigValue(t, got, "proxy", "http://127.0.0.1:9090")
	assertConfigValue(t, got, "refresh_account_interval_minute", 9)
	assertConfigValue(t, got, "image_concurrent_limit", 6)
	assertConfigValue(t, got, "user_default_concurrent_limit", 3)
	assertConfigValue(t, got, "user_default_rpm_limit", 45)
	assertConfigValue(t, got, "image_retention_days", 12)
	assertConfigValue(t, got, "log_retention_days", 30)
	assertConfigValue(t, got, "auto_remove_invalid_accounts", false)
	assertConfigValue(t, got, "auto_remove_rate_limited_accounts", true)
	if levels := strings.Join(store.LogLevels(), ","); levels != "debug,info" {
		t.Fatalf("LogLevels() = %q, want debug,info", levels)
	}

	for key, want := range map[string]string{
		"CHATGPT2API_BASE_URL":                          "https://new.example/root/",
		"CHATGPT2API_PROXY":                             "http://127.0.0.1:9090",
		"CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE":   "9",
		"CHATGPT2API_IMAGE_CONCURRENT_LIMIT":            "6",
		"CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT":     "3",
		"CHATGPT2API_USER_DEFAULT_RPM_LIMIT":            "45",
		"CHATGPT2API_IMAGE_RETENTION_DAYS":              "12",
		"CHATGPT2API_LOG_RETENTION_DAYS":                "30",
		"CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS":      "false",
		"CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS": "true",
		"CHATGPT2API_LOG_LEVELS":                        "debug,info",
	} {
		if gotEnv := os.Getenv(key); gotEnv != want {
			t.Fatalf("%s = %q, want %q", key, gotEnv, want)
		}
	}
}

func TestStoreKeepsDifferentExternalEnvironmentOverride(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(strings.Join([]string{
		"CHATGPT2API_BASE_URL=https://file.example",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("CHATGPT2API_ROOT", root)
	t.Setenv("CHATGPT2API_BASE_URL", "https://external.example")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	got, err := store.Update(map[string]any{"base_url": "https://saved.example"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertConfigValue(t, got, "base_url", "https://external.example")
	if gotEnv := os.Getenv("CHATGPT2API_BASE_URL"); gotEnv != "https://external.example" {
		t.Fatalf("CHATGPT2API_BASE_URL = %q, want external override unchanged", gotEnv)
	}
}

func TestNewStoreDiscoversEnvFromParentDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("CHATGPT2API_BASE_URL=https://parent.example\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	nested := filepath.Join(root, "cmd", "chatgpt2api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})
	unsetEnv(t, "CHATGPT2API_ROOT")
	unsetEnv(t, "CHATGPT2API_BASE_URL")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store.RootDir != root {
		t.Fatalf("RootDir = %q, want %q", store.RootDir, root)
	}
	if store.BaseURL() != "https://parent.example" {
		t.Fatalf("BaseURL() = %q", store.BaseURL())
	}
}

func assertConfigValue(t *testing.T, data map[string]any, key string, want any) {
	t.Helper()
	if got := data[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	original, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%s): %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, original)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func unsetLinuxDoEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CHATGPT2API_LINUXDO_ENABLED",
		"CHATGPT2API_LINUXDO_CLIENT_ID",
		"CHATGPT2API_LINUXDO_CLIENT_SECRET",
		"CHATGPT2API_LINUXDO_REDIRECT_URL",
		"CHATGPT2API_LINUXDO_AUTHORIZE_URL",
		"CHATGPT2API_LINUXDO_TOKEN_URL",
		"CHATGPT2API_LINUXDO_USERINFO_URL",
		"CHATGPT2API_LINUXDO_SCOPES",
		"CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL",
		"CHATGPT2API_LINUXDO_TOKEN_AUTH_METHOD",
		"CHATGPT2API_LINUXDO_USE_PKCE",
		"CHATGPT2API_LINUXDO_USERINFO_EMAIL_PATH",
		"CHATGPT2API_LINUXDO_USERINFO_ID_PATH",
		"CHATGPT2API_LINUXDO_USERINFO_USERNAME_PATH",
	} {
		unsetEnv(t, key)
	}
}
