package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"
)

func TestAppAuthAndSPACompatibility(t *testing.T) {
	originalVersion := version.Version
	version.Version = "test-build"
	t.Cleanup(func() { version.Version = originalVersion })

	app := newTestApp(t)
	defer app.Close()

	user, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if user["role"] != "user" {
		t.Fatalf("created user = %#v", user)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/users/"+user["id"].(string)+"/key", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("reveal user key status = %d body = %s", res.Code, res.Body.String())
	}
	var revealed map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &revealed); err != nil {
		t.Fatalf("reveal json: %v", err)
	}
	if revealed["key"] != rawKey {
		t.Fatalf("revealed key = %#v, want raw key", revealed["key"])
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/auth/login status = %d body = %s", res.Code, res.Body.String())
	}
	var login map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &login); err != nil {
		t.Fatalf("login json: %v", err)
	}
	if login["role"] != "user" {
		t.Fatalf("login role = %#v", login)
	}
	if login["version"] != "test-build" {
		t.Fatalf("login version = %#v", login["version"])
	}

	req = httptest.NewRequest(http.MethodGet, "/version", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/version status = %d body = %s", res.Code, res.Body.String())
	}
	var versionBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &versionBody); err != nil {
		t.Fatalf("version json: %v", err)
	}
	if versionBody["version"] != "test-build" {
		t.Fatalf("/version body = %#v", versionBody)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/announcements?target=login", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/api/announcements status = %d body = %s", res.Code, res.Body.String())
	}
	var announcementsBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &announcementsBody); err != nil {
		t.Fatalf("announcements json: %v", err)
	}
	if items := logItems(announcementsBody); len(items) != 0 {
		t.Fatalf("unexpected initial announcements = %#v", announcementsBody)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/announcements", strings.NewReader(`{"title":"通知 A","content":"今晚维护","show_login":true,"show_image":false}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create login announcement status = %d body = %s", res.Code, res.Body.String())
	}
	var createBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("create announcement json: %v", err)
	}
	createdItem, _ := createBody["item"].(map[string]any)
	createdID, _ := createdItem["id"].(string)
	if createdID == "" {
		t.Fatalf("missing created announcement id: %#v", createBody)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/announcements", strings.NewReader(`{"title":"通知 B","content":"画图页公告","show_login":false,"show_image":true}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create image announcement status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/announcements", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin list announcements status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &announcementsBody); err != nil {
		t.Fatalf("admin announcements json: %v", err)
	}
	if items := logItems(announcementsBody); len(items) != 2 {
		t.Fatalf("admin announcements length = %d body = %#v", len(items), announcementsBody)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/announcements?target=login", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public login announcements status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &announcementsBody); err != nil {
		t.Fatalf("public login announcements json: %v", err)
	}
	items := logItems(announcementsBody)
	if len(items) != 1 || items[0]["title"] != "通知 A" {
		t.Fatalf("unexpected public login announcements = %#v", announcementsBody)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/announcements/"+createdID, strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("disable announcement status = %d body = %s", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/announcements?target=login", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public login announcements after disable status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &announcementsBody); err != nil {
		t.Fatalf("public login announcements after disable json: %v", err)
	}
	if items := logItems(announcementsBody); len(items) != 0 {
		t.Fatalf("disabled announcement should be hidden: %#v", announcementsBody)
	}

	msgReq := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{"))
	msgReq.Header.Set("x-api-key", rawKey)
	msgRes := httptest.NewRecorder()
	app.Handler().ServeHTTP(msgRes, msgReq)
	if msgRes.Code != http.StatusBadRequest {
		t.Fatalf("x-api-key auth did not reach JSON validation, status = %d body = %s", msgRes.Code, msgRes.Body.String())
	}

	for _, path := range []string{"/", "/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "go-spa") {
			t.Fatalf("%s status/body = %d %q", path, res.Code, res.Body.String())
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", res.Code)
	}
}

func TestImageTaskFailureWritesCallLog(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/image-tasks/generations", strings.NewReader(`{"client_task_id":"task-log-test","prompt":"test image"}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("submit image task status = %d body = %s", res.Code, res.Body.String())
	}

	var logs map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req = httptest.NewRequest(http.MethodGet, "/api/logs?type=call", nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		res = httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("logs status = %d body = %s", res.Code, res.Body.String())
		}
		if err := json.Unmarshal(res.Body.Bytes(), &logs); err != nil {
			t.Fatalf("logs json: %v", err)
		}
		if len(logItems(logs)) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	items := logItems(logs)
	if len(items) == 0 {
		t.Fatalf("expected image task failure to write a call log, got %#v", logs)
	}
	item := items[0]
	if item["type"] != "call" || item["summary"] != "文生图调用失败" {
		t.Fatalf("unexpected call log item: %#v", item)
	}
	detail, _ := item["detail"].(map[string]any)
	if detail["endpoint"] != "/api/image-tasks/generations" || detail["status"] != "failed" {
		t.Fatalf("unexpected call log detail: %#v", detail)
	}
	if detail["key_name"] != "frontend" || detail["key_role"] != "user" {
		t.Fatalf("call log did not include user key identity: %#v", detail)
	}
}

func TestEmptyCollectionEndpointsReturnArrays(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	for _, tc := range []struct {
		name string
		path string
		keys []string
	}{
		{name: "accounts", path: "/api/accounts", keys: []string{"items"}},
		{name: "images", path: "/api/images", keys: []string{"items", "groups"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer admin-secret")
			res := httptest.NewRecorder()
			app.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("%s status = %d body = %s", tc.path, res.Code, res.Body.String())
			}
			var payload map[string]any
			if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
				t.Fatalf("%s json: %v", tc.path, err)
			}
			for _, key := range tc.keys {
				items, ok := payload[key].([]any)
				if !ok || items == nil || len(items) != 0 {
					t.Fatalf("%s %q = %#v, want empty array", tc.path, key, payload[key])
				}
			}
		})
	}
}

func TestLoginPageImageUploadSettings(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/app-meta", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("app meta status = %d body = %s", res.Code, res.Body.String())
	}
	var meta map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &meta); err != nil {
		t.Fatalf("app meta json: %v", err)
	}
	if meta["login_page_image_url"] != "" || meta["login_page_image_mode"] != "contain" {
		t.Fatalf("initial app meta = %#v", meta)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("login_page_image_action", "replace")
	_ = writer.WriteField("login_page_image_mode", "cover")
	_ = writer.WriteField("login_page_image_zoom", "1.25")
	_ = writer.WriteField("login_page_image_position_x", "40")
	_ = writer.WriteField("login_page_image_position_y", "60")
	part, err := writer.CreateFormFile("login_page_image_file", "panel.png")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if err := encodeHTTPTestPNG(part); err != nil {
		t.Fatalf("encode upload png: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/settings/login-page-image", body)
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("upload status = %d body = %s", res.Code, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("upload json: %v", err)
	}
	config, _ := payload["config"].(map[string]any)
	imageURL, _ := config["login_page_image_url"].(string)
	if !strings.HasPrefix(imageURL, "/login-page-images/") {
		t.Fatalf("uploaded image url = %#v in %#v", imageURL, payload)
	}
	if config["login_page_image_mode"] != "cover" || config["login_page_image_zoom"] != float64(1.25) {
		t.Fatalf("login page image config = %#v", config)
	}

	req = httptest.NewRequest(http.MethodGet, imageURL, nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("uploaded image static status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/app-meta", nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("app meta after upload status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &meta); err != nil {
		t.Fatalf("app meta after upload json: %v", err)
	}
	if meta["login_page_image_url"] != imageURL || meta["login_page_image_mode"] != "cover" {
		t.Fatalf("app meta after upload = %#v", meta)
	}
}

func TestImageManagementIsScopedByOwner(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	owner := service.AuthOwner{ID: "linuxdo:123", Name: "alice", Provider: service.AuthProviderLinuxDo}
	_, sessionKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession() error = %v", err)
	}
	aliceRel := "2026/04/29/alice.png"
	bobRel := "2026/04/29/bob.png"
	legacyRel := "2026/04/29/legacy.png"
	for _, rel := range []string{aliceRel, bobRel, legacyRel} {
		path := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir image dir: %v", err)
		}
		if err := writeHTTPTestPNG(path); err != nil {
			t.Fatalf("write image %s: %v", rel, err)
		}
	}
	app.images.RecordImageOwners([]string{aliceRel}, owner.ID)
	app.images.RecordImageOwners([]string{bobRel}, "linuxdo:456")

	req := httptest.NewRequest(http.MethodGet, "/api/images", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo images status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("linuxdo images json: %v", err)
	}
	items := logItems(list)
	if len(items) != 1 || items[0]["path"] != aliceRel {
		t.Fatalf("linuxdo scoped images = %#v", list)
	}
	if items[0]["owner_name"] != owner.Name || items[0]["visibility"] != service.ImageVisibilityPrivate {
		t.Fatalf("linuxdo image metadata = %#v", items[0])
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/images/visibility", strings.NewReader(`{"path":"`+aliceRel+`","visibility":"public"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo publish image status = %d body = %s", res.Code, res.Body.String())
	}
	var visibilityBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &visibilityBody); err != nil {
		t.Fatalf("visibility json: %v", err)
	}
	updatedItem, _ := visibilityBody["item"].(map[string]any)
	if updatedItem["visibility"] != service.ImageVisibilityPublic || updatedItem["owner_name"] != owner.Name {
		t.Fatalf("publish image response = %#v", visibilityBody)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/images/visibility", strings.NewReader(`{"path":"`+bobRel+`","visibility":"public"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("linuxdo publish other image status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images?scope=public", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public images status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("public images json: %v", err)
	}
	if items := logItems(list); len(items) != 1 || items[0]["path"] != aliceRel || items[0]["owner_name"] != owner.Name {
		t.Fatalf("public scoped images = %#v", list)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/images/visibility", strings.NewReader(`{"path":"`+aliceRel+`","visibility":"private"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo unpublish image status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images?scope=public", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("public images after unpublish status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("public images after unpublish json: %v", err)
	}
	if items := logItems(list); len(items) != 0 {
		t.Fatalf("unpublished image should leave public gallery: %#v", list)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+bobRel+`","`+aliceRel+`"]}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("linuxdo delete images status = %d body = %s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(filepath.Join(app.config.ImagesDir(), filepath.FromSlash(aliceRel))); err != nil {
		t.Fatalf("alice image should not be deleted by Linuxdo user, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(app.config.ImagesDir(), filepath.FromSlash(bobRel))); err != nil {
		t.Fatalf("bob image should not be deleted, stat error = %v", err)
	}

	_, localKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "local user", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey(local) error = %v", err)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+aliceRel+`"]}`))
	req.Header.Set("Authorization", "Bearer "+localKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("local user delete images status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/images", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin images status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("admin images json: %v", err)
	}
	if items := logItems(list); len(items) != 3 {
		t.Fatalf("admin should see owned and legacy images, got %#v", list)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"paths":["`+aliceRel+`"]}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin delete images status = %d body = %s", res.Code, res.Body.String())
	}
	var deleteBody map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &deleteBody); err != nil {
		t.Fatalf("admin delete images json: %v", err)
	}
	if deleteBody["deleted"] != float64(1) || deleteBody["missing"] != float64(0) {
		t.Fatalf("admin delete images body = %#v", deleteBody)
	}
	if _, err := os.Stat(filepath.Join(app.config.ImagesDir(), filepath.FromSlash(aliceRel))); !os.IsNotExist(err) {
		t.Fatalf("alice image should be deleted by admin, stat error = %v", err)
	}
}

func TestImageThumbnailsAreGeneratedOnDemand(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	rel := "2026/04/29/sample.png"
	imagePath := filepath.Join(app.config.ImagesDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("mkdir image dir: %v", err)
	}
	if err := writeHTTPTestPNG(imagePath); err != nil {
		t.Fatalf("write image: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/images", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("/api/images status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("/api/images json: %v", err)
	}
	items := logItems(list)
	if len(items) != 1 {
		t.Fatalf("/api/images items = %#v", list)
	}
	thumbnailURL, _ := items[0]["thumbnail_url"].(string)
	if !strings.Contains(thumbnailURL, "/image-thumbnails/") {
		t.Fatalf("thumbnail_url = %q, want lazy thumbnail route", thumbnailURL)
	}
	parsedThumbnailURL, err := url.Parse(thumbnailURL)
	if err != nil {
		t.Fatalf("parse thumbnail URL: %v", err)
	}
	thumbPath := filepath.Join(app.config.ImageThumbnailsDir(), filepath.FromSlash(rel)+".webp")
	if _, err := os.Stat(thumbPath); !os.IsNotExist(err) {
		t.Fatalf("/api/images should not create thumbnail synchronously, stat error = %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, parsedThumbnailURL.Path, nil)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("thumbnail status = %d body = %s", res.Code, res.Body.String())
	}
	if res.Body.Len() == 0 {
		t.Fatal("thumbnail body is empty")
	}
	if _, err := os.Stat(thumbPath); err != nil {
		t.Fatalf("thumbnail was not created on demand: %v", err)
	}
}

func TestImageThumbnailRejectsTraversal(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	outsideThumbnailRoot := filepath.Join(app.config.DataDir, "secret.png.webp")
	if err := os.WriteFile(outsideThumbnailRoot, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside thumbnail root: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/image-thumbnails/../secret.png.webp", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("thumbnail traversal status = %d body = %q, want 404", res.Code, res.Body.String())
	}
}

func TestLinuxDoUserCanManageOwnKeys(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	owner := service.AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: service.AuthProviderLinuxDo, LinuxDoLevel: "3"}
	_, sessionKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession() error = %v", err)
	}
	_, otherKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "other user key", service.AuthOwner{ID: "linuxdo:456", Name: "other", Provider: service.AuthProviderLinuxDo})
	if err != nil || otherKey == "" {
		t.Fatalf("CreateAPIKey(other) key=%q err=%v", otherKey, err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/users", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo initial list status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("initial list json: %v", err)
	}
	if rawItems, ok := list["items"].([]any); !ok || len(rawItems) != 0 {
		t.Fatalf("linuxdo initial list should be empty array, got %#v", list)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(`{"name":"linuxdo api"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo create key status = %d body = %s", res.Code, res.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("create key json: %v", err)
	}
	item, _ := created["item"].(map[string]any)
	if item["owner_id"] != owner.ID || item["provider"] != service.AuthProviderLinuxDo {
		t.Fatalf("created key owner = %#v", item)
	}
	firstKey, _ := created["key"].(string)
	firstID, _ := item["id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(`{"name":"linuxdo api refreshed"}`))
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo reset key status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("reset key json: %v", err)
	}
	item, _ = created["item"].(map[string]any)
	resetKey, _ := created["key"].(string)
	if item["id"] != firstID || resetKey == "" || resetKey == firstKey {
		t.Fatalf("reset key did not rotate in place: item=%#v key=%q first=%q", item, resetKey, firstKey)
	}
	if app.auth.Authenticate(firstKey) != nil {
		t.Fatal("old Linuxdo API key still authenticated after reset")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/users", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("linuxdo list keys status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("list keys json: %v", err)
	}
	if items := logItems(list); len(items) != 1 || items[0]["owner_id"] != owner.ID {
		t.Fatalf("linuxdo scoped list = %#v", list)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/users", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin list keys status = %d body = %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("admin list json: %v", err)
	}
	if items := logItems(list); len(items) != 2 {
		t.Fatalf("admin should see all API keys, got %#v", list)
	}

	_, unownedKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "legacy user", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey(unowned) error = %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(`{"name":"should fail"}`))
	req.Header.Set("Authorization", "Bearer "+unownedKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("unowned user key manage status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestAdminUsersManageLinuxDoUsers(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	owner := service.AuthOwner{ID: "linuxdo:123", Name: "linuxdo_user", Provider: service.AuthProviderLinuxDo, LinuxDoLevel: "3"}
	_, sessionKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession() error = %v", err)
	}
	_, ownerAPIKey, err := app.auth.UpsertAPIKeyForOwner("", owner)
	if err != nil {
		t.Fatalf("UpsertAPIKeyForOwner() error = %v", err)
	}
	local, localKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "local user", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey(local) error = %v", err)
	}
	localID, _ := local["id"].(string)
	app.logs.Add(service.LogTypeCall, "文生图调用完成", map[string]any{
		"subject_id":  owner.ID,
		"key_id":      "linuxdo-session",
		"status":      "success",
		"endpoint":    "/v1/images/generations",
		"duration_ms": 120,
		"urls":        []string{"https://example.test/a.png", "https://example.test/b.png"},
	})
	app.logs.Add(service.LogTypeCall, "文生图调用失败", map[string]any{
		"subject_id": owner.ID,
		"key_id":     "linuxdo-session",
		"status":     "failed",
		"endpoint":   "/v1/images/generations",
	})
	app.logs.Add(service.LogTypeCall, "图生图调用完成", map[string]any{
		"key_id":   localID,
		"status":   "success",
		"endpoint": "/api/image-tasks/edits",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("linuxdo admin users status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin users status = %d body = %s", res.Code, res.Body.String())
	}
	var list map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("admin users json: %v", err)
	}
	linuxdoUser := findHTTPItem(logItems(list), owner.ID)
	if linuxdoUser == nil || linuxdoUser["provider"] != service.AuthProviderLinuxDo || linuxdoUser["has_session"] != true || linuxdoUser["has_api_key"] != true {
		t.Fatalf("linuxdo managed user = %#v in %#v", linuxdoUser, list)
	}
	if linuxdoUser["linuxdo_level"] != "3" {
		t.Fatalf("linuxdo level = %#v", linuxdoUser)
	}
	localUser := findHTTPItem(logItems(list), localID)
	if localUser == nil || localUser["provider"] != service.AuthProviderLocal || localUser["has_api_key"] != true {
		t.Fatalf("local managed user = %#v in %#v", localUser, list)
	}
	if linuxdoUser["call_count"] != float64(2) || linuxdoUser["success_count"] != float64(1) || linuxdoUser["failure_count"] != float64(1) || linuxdoUser["quota_used"] != float64(2) {
		t.Fatalf("linuxdo usage stats = %#v", linuxdoUser)
	}
	if curve, ok := linuxdoUser["usage_curve"].([]any); !ok || len(curve) != 14 {
		t.Fatalf("linuxdo usage curve = %#v", linuxdoUser["usage_curve"])
	}
	if localUser["call_count"] != float64(1) || localUser["quota_used"] != float64(1) {
		t.Fatalf("local usage stats = %#v", localUser)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(`{"name":"created local"}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("create managed user status = %d body = %s", res.Code, res.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("create managed user json: %v", err)
	}
	createdKey, _ := created["key"].(string)
	createdItem, _ := created["item"].(map[string]any)
	if createdKey == "" || createdItem["name"] != "created local" || createdItem["has_api_key"] != true {
		t.Fatalf("create managed user body = %#v", created)
	}
	if identity := app.auth.Authenticate(createdKey); identity == nil || identity.Name != "created local" {
		t.Fatalf("created managed user identity = %#v", identity)
	}
	createdID, _ := createdItem["id"].(string)
	createdPath := "/api/admin/users/" + url.PathEscape(createdID)

	req = httptest.NewRequest(http.MethodGet, createdPath+"/key", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("reveal local managed key status = %d body = %s", res.Code, res.Body.String())
	}
	var revealed map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &revealed); err != nil {
		t.Fatalf("reveal local managed key json: %v", err)
	}
	if revealed["key"] != createdKey {
		t.Fatalf("revealed local managed key = %#v want %q", revealed["key"], createdKey)
	}

	req = httptest.NewRequest(http.MethodPost, createdPath+"/reset-key", strings.NewReader(`{"name":"rotated local"}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("reset local managed key status = %d body = %s", res.Code, res.Body.String())
	}
	var reset map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &reset); err != nil {
		t.Fatalf("reset local managed key json: %v", err)
	}
	rotatedLocalKey, _ := reset["key"].(string)
	if rotatedLocalKey == "" || rotatedLocalKey == createdKey {
		t.Fatalf("reset local managed key body = %#v", reset)
	}
	if app.auth.Authenticate(createdKey) != nil {
		t.Fatal("old created local key still authenticates after reset")
	}
	if identity := app.auth.Authenticate(rotatedLocalKey); identity == nil || identity.ID != createdID {
		t.Fatalf("rotated local managed key identity = %#v", identity)
	}

	ownerPath := "/api/admin/users/" + url.PathEscape(owner.ID)
	req = httptest.NewRequest(http.MethodPost, ownerPath+"/reset-key", strings.NewReader(`{"name":"managed token"}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("reset linuxdo managed key status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, ownerPath+"/key", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("reveal linuxdo managed key status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, ownerPath, strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("disable managed user status = %d body = %s", res.Code, res.Body.String())
	}
	if app.auth.Authenticate(sessionKey) != nil || app.auth.Authenticate(ownerAPIKey) != nil {
		t.Fatal("disabled linuxdo user credentials still authenticate")
	}
	if app.auth.Authenticate(localKey) == nil {
		t.Fatal("disabling linuxdo user should not affect local user")
	}
	disabledLoginItem, disabledLoginKey, err := app.auth.UpsertLinuxDoSession(owner)
	if err != nil {
		t.Fatalf("UpsertLinuxDoSession(disabled) error = %v", err)
	}
	if disabledLoginItem["enabled"] != false {
		t.Fatalf("disabled linuxdo login item = %#v", disabledLoginItem)
	}
	if app.auth.Authenticate(disabledLoginKey) != nil {
		t.Fatal("disabled linuxdo user authenticated after a new login")
	}

	req = httptest.NewRequest(http.MethodPost, ownerPath, strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("enable managed user status = %d body = %s", res.Code, res.Body.String())
	}
	if app.auth.Authenticate(disabledLoginKey) == nil || app.auth.Authenticate(ownerAPIKey) == nil {
		t.Fatal("enabled linuxdo user credentials should authenticate")
	}

	req = httptest.NewRequest(http.MethodDelete, ownerPath, nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("delete managed user status = %d body = %s", res.Code, res.Body.String())
	}
	if app.auth.Authenticate(disabledLoginKey) != nil || app.auth.Authenticate(ownerAPIKey) != nil {
		t.Fatal("deleted linuxdo user credentials still authenticate")
	}
	if app.auth.Authenticate(localKey) == nil {
		t.Fatal("deleting linuxdo user should not affect local user")
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("delete managed user json: %v", err)
	}
	if findHTTPItem(logItems(list), owner.ID) != nil {
		t.Fatalf("deleted linuxdo user still listed: %#v", list)
	}
}

func TestLinuxDoOAuthCallbackCreatesSession(t *testing.T) {
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if r.Form.Get("code") != "oauth-code" || r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "client-secret" {
				t.Fatalf("unexpected token form = %#v", r.Form)
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"access_token": "linuxdo-access", "token_type": "Bearer", "expires_in": 3600})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer linuxdo-access" {
				t.Fatalf("userinfo authorization = %q", r.Header.Get("Authorization"))
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"id": 123, "username": "linuxdo_user", "trust_level": 2})
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauthServer.Close()

	t.Setenv("CHATGPT2API_LINUXDO_ENABLED", "true")
	t.Setenv("CHATGPT2API_LINUXDO_CLIENT_ID", "client-id")
	t.Setenv("CHATGPT2API_LINUXDO_CLIENT_SECRET", "client-secret")
	t.Setenv("CHATGPT2API_LINUXDO_AUTHORIZE_URL", oauthServer.URL+"/authorize")
	t.Setenv("CHATGPT2API_LINUXDO_TOKEN_URL", oauthServer.URL+"/token")
	t.Setenv("CHATGPT2API_LINUXDO_USERINFO_URL", oauthServer.URL+"/user")
	t.Setenv("CHATGPT2API_LINUXDO_REDIRECT_URL", "http://chatgpt2api.test/auth/linuxdo/oauth/callback")
	t.Setenv("CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL", "/auth/linuxdo/callback")

	app := newTestApp(t)
	defer app.Close()

	req := httptest.NewRequest(http.MethodGet, "/auth/linuxdo/start?redirect=/settings", nil)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("start status = %d body = %s", res.Code, res.Body.String())
	}
	authorizeURL, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize location: %v", err)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" || authorizeURL.Query().Get("client_id") != "client-id" {
		t.Fatalf("authorize location = %s", authorizeURL.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/linuxdo/oauth/callback?code=oauth-code&state="+url.QueryEscape(state), nil)
	for _, cookie := range res.Result().Cookies() {
		req.AddCookie(cookie)
	}
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("callback status = %d body = %s", res.Code, res.Body.String())
	}
	callbackLocation := res.Header().Get("Location")
	if strings.Contains(callbackLocation, "%25") {
		t.Fatalf("callback location double-encoded fragment values: %s", callbackLocation)
	}
	callbackURL, err := url.Parse(callbackLocation)
	if err != nil {
		t.Fatalf("parse callback location: %v", err)
	}
	fragment, err := url.ParseQuery(callbackURL.Fragment)
	if err != nil {
		t.Fatalf("parse callback fragment: %v", err)
	}
	sessionKey := fragment.Get("key")
	if sessionKey == "" || fragment.Get("subject_id") != "linuxdo:123" || fragment.Get("redirect") != "/settings" {
		t.Fatalf("callback fragment = %#v", fragment)
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.Header.Set("Authorization", "Bearer "+sessionKey)
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("login with linuxdo session status = %d body = %s", res.Code, res.Body.String())
	}
	var login map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &login); err != nil {
		t.Fatalf("login json: %v", err)
	}
	if login["subject_id"] != "linuxdo:123" || login["provider"] != service.AuthProviderLinuxDo || login["name"] != "linuxdo_user" {
		t.Fatalf("login response = %#v", login)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("admin users after linuxdo oauth status = %d body = %s", res.Code, res.Body.String())
	}
	var users map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &users); err != nil {
		t.Fatalf("admin users json: %v", err)
	}
	linuxdoUser := findHTTPItem(logItems(users), "linuxdo:123")
	if linuxdoUser == nil || linuxdoUser["linuxdo_level"] != "2" {
		t.Fatalf("oauth linuxdo user level = %#v", linuxdoUser)
	}
}

func TestImageTaskPollingDisablesCaching(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/image-tasks?ids=missing", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("image task list status = %d body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := res.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
}

func TestModelsCallLogIncludesUserKeyName(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()

	_, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "frontend", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("models status = %d body = %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/logs?type=call", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	res = httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("logs status = %d body = %s", res.Code, res.Body.String())
	}
	var logs map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &logs); err != nil {
		t.Fatalf("logs json: %v", err)
	}
	items := logItems(logs)
	if len(items) == 0 {
		t.Fatalf("expected models call to write a call log, got %#v", logs)
	}
	detail, _ := items[0]["detail"].(map[string]any)
	if detail["endpoint"] != "/v1/models" || detail["key_name"] != "frontend" || detail["key_role"] != "user" {
		t.Fatalf("models call log did not include user key identity: %#v", detail)
	}
}

func logItems(payload map[string]any) []map[string]any {
	rawItems, _ := payload["items"].([]any)
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

func findHTTPItem(items []map[string]any, id string) map[string]any {
	for _, item := range items {
		if item["id"] == id {
			return item
		}
	}
	return nil
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	t.Setenv("CHATGPT2API_AUTH_KEY", "admin-secret")
	t.Setenv("STORAGE_BACKEND", "json")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("GIT_REPO_URL", "")
	if err := os.MkdirAll(filepath.Join(root, "web_dist", "assets"), 0o755); err != nil {
		t.Fatalf("mkdir web_dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "web_dist", "index.html"), []byte("<html>go-spa</html>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	app, err := NewApp()
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	app.engine.ListModelsFunc = func(context.Context) (map[string]any, error) {
		return map[string]any{"object": "list", "data": []map[string]any{}}, nil
	}
	return app
}

func writeHTTPTestPNG(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return encodeHTTPTestPNG(file)
}

func encodeHTTPTestPNG(file interface {
	Write([]byte) (int, error)
}) error {
	img := image.NewRGBA(image.Rect(0, 0, 12, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 180, A: 255})
		}
	}
	return png.Encode(file, img)
}
