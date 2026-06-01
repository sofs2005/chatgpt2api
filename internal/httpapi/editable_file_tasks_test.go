package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
)

func TestEditableFileRoutesRequireAuthAndServeFilesSafely(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	app.editableFiles = newTestEditableFileTaskService(t)

	user, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "editable-file-test", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if user["role"] != "user" {
		t.Fatalf("created user role = %#v", user["role"])
	}
	bearer := "Bearer " + rawKey

	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/ppt/generations", strings.NewReader(`{"prompt":"make slides"}`))
	unauthorized.Header.Set("Content-Type", "application/json")
	unauthorizedRes := httptest.NewRecorder()
	app.Handler().ServeHTTP(unauthorizedRes, unauthorized)
	if unauthorizedRes.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body = %s", unauthorizedRes.Code, unauthorizedRes.Body.String())
	}

	submit := httptest.NewRequest(http.MethodPost, "/v1/ppt/generations", strings.NewReader(`{"prompt":"make slides","client_task_id":"client-1"}`))
	submit.Header.Set("Authorization", bearer)
	submit.Header.Set("Content-Type", "application/json")
	submitRes := httptest.NewRecorder()
	app.Handler().ServeHTTP(submitRes, submit)
	if submitRes.Code != http.StatusOK {
		t.Fatalf("submit status = %d body = %s", submitRes.Code, submitRes.Body.String())
	}
	var task map[string]any
	if err := json.Unmarshal(submitRes.Body.Bytes(), &task); err != nil {
		t.Fatalf("submit json: %v", err)
	}
	if task["status"] != service.TaskStatusQueued {
		t.Fatalf("submit status = %#v, want queued", task["status"])
	}
	if task["kind"] != "ppt" {
		t.Fatalf("submit kind = %#v, want ppt", task["kind"])
	}

	waitForHTTPEditableFileTaskStatus(t, app, bearer, "client-1", service.TaskStatusSuccess)

	listReq := httptest.NewRequest(http.MethodGet, "/v1/editable-file-tasks?ids=client-1,missing", nil)
	listReq.Header.Set("Authorization", bearer)
	listRes := httptest.NewRecorder()
	app.Handler().ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listRes.Code, listRes.Body.String())
	}
	var listBody map[string]any
	if err := json.Unmarshal(listRes.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("list json: %v", err)
	}
	items := listBody["items"].([]any)
	missing := listBody["missing_ids"].([]any)
	if len(items) != 1 || len(missing) != 1 || missing[0] != "missing" {
		t.Fatalf("list body = %#v", listBody)
	}
	item := items[0].(map[string]any)
	result := item["result"].(map[string]any)
	primaryPath := result["primary_path"].(string)
	if primaryPath == "" {
		t.Fatalf("primary_path missing: %#v", result)
	}

	fileReq := httptest.NewRequest(http.MethodGet, "/files/"+url.PathEscape(primaryPath), nil)
	fileRes := httptest.NewRecorder()
	app.Handler().ServeHTTP(fileRes, fileReq)
	if fileRes.Code != http.StatusOK {
		t.Fatalf("file download status = %d body = %s", fileRes.Code, fileRes.Body.String())
	}
	if !strings.Contains(fileRes.Body.String(), "ppt-bytes") {
		t.Fatalf("file download body = %q", fileRes.Body.String())
	}

	for _, path := range []string{"/files/%2e%2e%2fsecret.txt", "/files/..%2Fsecret.txt", "/files/%2Fetc%2Fpasswd"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("path %s status = %d, want 404", path, res.Code)
		}
	}
}

func TestEditableFilePSDRequiresImages(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	app.editableFiles = newTestEditableFileTaskService(t)
	user, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "editable-file-test", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if user["role"] != "user" {
		t.Fatalf("created user role = %#v", user["role"])
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/psd/generations", strings.NewReader(`{"prompt":"make psd","client_task_id":"client-psd"}`))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("psd empty images status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestEditableFileRoutesRejectWrongMethods(t *testing.T) {
	app := newTestApp(t)
	defer app.Close()
	app.editableFiles = newTestEditableFileTaskService(t)
	user, rawKey, err := app.auth.CreateAPIKey(service.AuthRoleUser, "editable-file-test", service.AuthOwner{})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	_ = user

	req := httptest.NewRequest(http.MethodGet, "/v1/ppt/generations", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("wrong method status = %d, want 404", res.Code)
	}
}

func newTestEditableFileTaskService(t *testing.T) *service.EditableFileTaskService {
	t.Helper()
	root := t.TempDir()
	backend, err := storage.NewDatabaseBackend("sqlite:///" + filepath.ToSlash(filepath.Join(root, "editable-files.db")))
	if err != nil {
		t.Fatalf("NewDatabaseBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	return service.NewEditableFileTaskService(backend, root, func(ctx context.Context, kind, prompt string, base64Images []string, outputDir string) (service.EditableFileRunResult, error) {
		if err := os.WriteFile(filepath.Join(outputDir, "deck.pptx"), []byte("ppt-bytes"), 0o644); err != nil {
			return service.EditableFileRunResult{}, err
		}
		if kind == "psd" {
			if len(base64Images) == 0 {
				return service.EditableFileRunResult{}, context.Canceled
			}
		}
		return service.EditableFileRunResult{
			ConversationID: "conv-http",
			PrimaryPath:    filepath.Join(outputDir, "deck.pptx"),
		}, nil
	})
}

func waitForHTTPEditableFileTaskStatus(t *testing.T, app *App, bearer, id, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/v1/editable-file-tasks?ids="+url.QueryEscape(id), nil)
		req.Header.Set("Authorization", bearer)
		res := httptest.NewRecorder()
		app.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("poll status = %d body = %s", res.Code, res.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("poll json: %v", err)
		}
		items := body["items"].([]any)
		if len(items) == 1 && items[0].(map[string]any)["status"] == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for editable file task %s status %s", id, want)
}
