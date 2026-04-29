package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"chatgpt2api/internal/config"
	"chatgpt2api/internal/protocol"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"

	_ "github.com/HugoSmits86/nativewebp"
)

const maxLoginPageImageSize = 10 << 20

type App struct {
	config     *config.Store
	auth       *service.AuthService
	accounts   *service.AccountService
	logs       *service.LogService
	logger     *service.Logger
	proxy      *service.ProxyService
	engine     *protocol.Engine
	images     *service.ImageService
	tasks      *service.ImageTaskService
	announce   *service.AnnouncementService
	cpa        *service.CPAConfig
	cpaImport  *service.CPAImportService
	sub2       *service.Sub2APIConfig
	sub2Import *service.Sub2APIService
	register   *service.RegisterService
	cancel     context.CancelFunc
}

func NewApp() (*App, error) {
	cfg, err := config.NewStore()
	if err != nil {
		return nil, err
	}
	storageBackend, err := cfg.StorageBackend()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	logs := service.NewLogService(cfg.DataDir, storageBackend)
	logger := service.NewLogger(cfg.LogLevels)
	proxy := service.NewProxyService(cfg)
	accounts := service.NewAccountService(storageBackend, cfg, proxy, logs)
	auth := service.NewAuthService(storageBackend)
	documentStore, _ := storageBackend.(storage.JSONDocumentBackend)
	engine := &protocol.Engine{Accounts: accounts, Config: cfg, Storage: documentStore, Proxy: proxy, Logger: logger}
	app := &App{config: cfg, auth: auth, accounts: accounts, logs: logs, logger: logger, proxy: proxy, engine: engine, images: service.NewImageService(cfg, storageBackend), announce: service.NewAnnouncementService(cfg.DataDir, storageBackend), cpa: service.NewCPAConfig(cfg.DataDir, storageBackend), sub2: service.NewSub2APIConfig(cfg.DataDir, storageBackend), cancel: cancel}
	app.cpaImport = service.NewCPAImportService(app.cpa, accounts, proxy)
	app.sub2Import = service.NewSub2APIService(app.sub2, accounts)
	app.register = service.NewRegisterService(cfg.DataDir, accounts, storageBackend)
	app.tasks = service.NewStoredImageTaskService(filepath.Join(cfg.DataDir, "image_tasks.json"), storageBackend,
		func(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
			return app.runLoggedImageTask(ctx, identity, payload, "/api/image-tasks/generations", "文生图", func(ctx context.Context, payload map[string]any) (map[string]any, error) {
				result, _, err := engine.HandleImageGenerations(ctx, payload)
				return result, err
			})
		},
		func(ctx context.Context, identity service.Identity, payload map[string]any) (map[string]any, error) {
			return app.runLoggedImageTask(ctx, identity, payload, "/api/image-tasks/edits", "图生图", func(ctx context.Context, payload map[string]any) (map[string]any, error) {
				images, _ := payload["images"].([]protocol.UploadedImage)
				result, _, err := engine.HandleImageEdits(ctx, payload, images)
				return result, err
			})
		},
		cfg.ImageRetentionDays,
		cfg.ImageConcurrentLimit,
		cfg.UserDefaultConcurrentLimit,
		cfg.UserDefaultRPMLimit,
	)
	accounts.StartLimitedWatcher(ctx, time.Duration(cfg.RefreshAccountIntervalMinute())*time.Minute)
	cfg.CleanupOldImages()
	return app, nil
}

func (a *App) Close() {
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *App) Handler() http.Handler {
	return http.HandlerFunc(a.serveHTTP)
}

func (a *App) serveHTTP(w http.ResponseWriter, r *http.Request) {
	a.applyCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := r.URL.Path
	switch {
	case path == "/v1/models" && r.Method == http.MethodGet:
		a.handleModels(w, r)
	case path == "/v1/images/generations" && r.Method == http.MethodPost:
		a.handleImageGenerations(w, r)
	case path == "/v1/images/edits" && r.Method == http.MethodPost:
		a.handleImageEdits(w, r)
	case path == "/v1/chat/completions" && r.Method == http.MethodPost:
		a.handleChatCompletions(w, r)
	case path == "/v1/responses" && r.Method == http.MethodPost:
		a.handleResponses(w, r)
	case path == "/v1/messages" && r.Method == http.MethodPost:
		a.handleMessages(w, r)
	case path == "/auth/login" && r.Method == http.MethodPost:
		a.handleLogin(w, r)
	case path == "/auth/providers":
		a.handleAuthProviders(w, r)
	case path == "/auth/linuxdo/start":
		a.handleLinuxDoOAuthStart(w, r)
	case path == "/auth/linuxdo/oauth/callback":
		a.handleLinuxDoOAuthCallback(w, r)
	case path == "/version" && r.Method == http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"version": version.Get()})
	case path == "/api/announcements":
		a.handlePublicAnnouncements(w, r)
	case path == "/api/admin/announcements" || strings.HasPrefix(path, "/api/admin/announcements/"):
		a.handleAdminAnnouncements(w, r)
	case path == "/api/admin/users" || strings.HasPrefix(path, "/api/admin/users/"):
		a.handleAdminUsers(w, r)
	case strings.HasPrefix(path, "/api/auth/users"):
		a.handleUserKeys(w, r)
	case path == "/api/accounts" || strings.HasPrefix(path, "/api/accounts/"):
		a.handleAccounts(w, r)
	case strings.HasPrefix(path, "/api/cpa/pools"):
		a.handleCPA(w, r)
	case strings.HasPrefix(path, "/api/sub2api/servers"):
		a.handleSub2API(w, r)
	case strings.HasPrefix(path, "/api/image-tasks"):
		a.handleImageTasks(w, r)
	case strings.HasPrefix(path, "/api/register"):
		a.handleRegister(w, r)
	case path == "/api/settings":
		a.handleSettings(w, r)
	case path == "/api/settings/login-page-image":
		a.handleLoginPageImageSettings(w, r)
	case path == "/api/app-meta" && r.Method == http.MethodGet:
		a.handleAppMeta(w, r)
	case path == "/api/images":
		a.handleImages(w, r)
	case path == "/api/logs" && r.Method == http.MethodGet:
		a.handleLogs(w, r)
	case path == "/api/proxy" || path == "/api/proxy/test":
		a.handleProxy(w, r)
	case path == "/api/storage/info" && r.Method == http.MethodGet:
		a.handleStorageInfo(w, r)
	case strings.HasPrefix(path, "/images/"):
		http.StripPrefix("/images/", http.FileServer(http.Dir(a.config.ImagesDir()))).ServeHTTP(w, r)
	case strings.HasPrefix(path, "/image-thumbnails/"):
		a.handleImageThumbnail(w, r)
	case strings.HasPrefix(path, "/login-page-images/"):
		http.StripPrefix("/login-page-images/", http.FileServer(http.Dir(a.config.LoginPageImagesDir()))).ServeHTTP(w, r)
	default:
		a.serveWeb(w, r)
	}
}

func (a *App) applyCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
}

func (a *App) handleModels(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	result, err := a.engine.ListModels(r.Context())
	a.writeProtocol(w, r, result, nil, err, "openai", "/v1/models", "models", identity, "模型列表")
}

func (a *App) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	body["owner_id"] = identityScope(identity)
	body["base_url"] = a.resolveImageBaseURL(r)
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	result, stream, err := a.engine.HandleImageGenerations(r.Context(), body)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/images/generations", model, identity, "文生图")
}

func (a *App) handleImageEdits(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, images, err := readMultipartImageBody(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if n := util.ToInt(body["n"], 1); n < 1 || n > 4 {
		util.WriteError(w, http.StatusBadRequest, "n must be between 1 and 4")
		return
	}
	if len(images) == 0 {
		util.WriteError(w, http.StatusBadRequest, "image file is required")
		return
	}
	body["owner_id"] = identityScope(identity)
	body["base_url"] = a.resolveImageBaseURL(r)
	model := firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto)
	result, stream, err := a.engine.HandleImageEdits(r.Context(), body, images)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/images/edits", model, identity, "图生图")
}

func (a *App) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	body["owner_id"] = identityScope(identity)
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	result, stream, err := a.engine.HandleChatCompletions(r.Context(), body)
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/chat/completions", model, identity, "文本生成")
}

func (a *App) handleResponses(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	body["owner_id"] = identityScope(identity)
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	result, stream, err := a.engine.HandleResponsesScoped(r.Context(), body, identityScope(identity))
	a.writeProtocol(w, r, result, stream, err, "openai", "/v1/responses", model, identity, "Responses")
}

func (a *App) handleMessages(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" && r.Header.Get("x-api-key") != "" {
		authHeader = "Bearer " + r.Header.Get("x-api-key")
	}
	identity, ok := a.requireIdentity(w, r, authHeader)
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	model := firstNonEmpty(util.Clean(body["model"]), "auto")
	result, stream, err := a.engine.HandleMessages(r.Context(), body)
	a.writeProtocol(w, r, result, stream, err, "anthropic", "/v1/messages", model, identity, "Messages")
}

func (a *App) writeProtocol(w http.ResponseWriter, r *http.Request, result map[string]any, stream *protocol.StreamResult, err error, sseKind, endpoint, model string, identity service.Identity, summary string) {
	start := time.Now()
	if err != nil {
		a.logCall(identity, summary, endpoint, model, start, "failed", err.Error(), nil)
		a.writeProtocolError(w, err)
		return
	}
	if stream == nil {
		urls := collectURLs(result)
		a.recordImageOwners(identity, urls)
		a.logCall(identity, summary, endpoint, model, start, "success", "", urls)
		util.WriteJSON(w, http.StatusOK, result)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	if stream.Kind == "anthropic" || sseKind == "anthropic" {
		var urls []string
		for item := range stream.Items {
			urls = append(urls, collectURLs(item)...)
			event := firstNonEmpty(util.Clean(item["type"]), "message_delta")
			fmt.Fprintf(w, "event: %s\n", event)
			fmt.Fprintf(w, "data: %s\n\n", jsonString(item))
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err := <-stream.Err; err != nil {
			a.recordImageOwners(identity, urls)
			a.logCall(identity, summary, endpoint, model, start, "failed", err.Error(), urls)
			fmt.Fprintf(w, "event: error\n")
			fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]any{"type": "error", "error": map[string]any{"type": fmt.Sprintf("%T", err), "message": err.Error()}}))
			return
		}
		a.recordImageOwners(identity, urls)
		a.logCall(identity, summary, endpoint, model, start, "success", "", urls)
		return
	}
	fmt.Fprint(w, ": stream-open\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	var urls []string
	for item := range stream.Items {
		urls = append(urls, collectURLs(item)...)
		fmt.Fprintf(w, "data: %s\n\n", jsonString(item))
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := <-stream.Err; err != nil {
		a.recordImageOwners(identity, urls)
		a.logCall(identity, summary, endpoint, model, start, "failed", err.Error(), urls)
		fmt.Fprintf(w, "data: %s\n\n", jsonString(openAIErrorForStream(err)))
	} else {
		a.recordImageOwners(identity, urls)
		a.logCall(identity, summary, endpoint, model, start, "success", "", urls)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func (a *App) writeProtocolError(w http.ResponseWriter, err error) {
	var httpErr protocol.HTTPError
	if errors.As(err, &httpErr) {
		util.WriteError(w, httpErr.Status, httpErr.Message)
		return
	}
	var imageErr *protocol.ImageGenerationError
	if errors.As(err, &imageErr) {
		util.WriteJSON(w, imageErr.StatusCode, imageErr.OpenAIError())
		return
	}
	message := err.Error()
	if strings.Contains(strings.ToLower(message), "no available image quota") {
		util.WriteJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{"message": "no available image quota", "type": "insufficient_quota", "param": nil, "code": "insufficient_quota"}})
		return
	}
	util.WriteJSON(w, http.StatusBadGateway, map[string]any{"detail": map[string]any{"error": message}})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"version":         version.Get(),
		"role":            identity.Role,
		"subject_id":      identity.ID,
		"name":            identity.Name,
		"provider":        identity.Provider,
		"credential_id":   identity.CredentialID,
		"credential_name": identity.CredentialName,
	})
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"config": a.config.Get()})
	case http.MethodPost:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		updated, err := a.config.Update(body)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"config": updated})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleAppMeta(w http.ResponseWriter, r *http.Request) {
	util.WriteJSON(w, http.StatusOK, map[string]any{
		"app_title":                   "chatgpt2api",
		"project_name":                "chatgpt2api",
		"login_page_image_url":        a.config.LoginPageImageURL(),
		"login_page_image_mode":       a.config.LoginPageImageMode(),
		"login_page_image_zoom":       a.config.LoginPageImageZoom(),
		"login_page_image_position_x": a.config.LoginPageImagePositionX(),
		"login_page_image_position_y": a.config.LoginPageImagePositionY(),
	})
}

func (a *App) handleLoginPageImageSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(maxLoginPageImageSize + (1 << 20)); err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	currentImageURL := a.config.LoginPageImageURL()
	nextImageURL := strings.TrimSpace(r.FormValue("login_page_image_url"))
	uploadedImageURL := ""
	switch strings.ToLower(strings.TrimSpace(r.FormValue("login_page_image_action"))) {
	case "remove":
		nextImageURL = ""
	case "replace":
		fileHeader := firstMultipartFile(r.MultipartForm, "login_page_image_file")
		if fileHeader == nil {
			util.WriteError(w, http.StatusBadRequest, "login page image file is required")
			return
		}
		storedURL, err := a.storeLoginPageImage(fileHeader)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		nextImageURL = storedURL
		uploadedImageURL = storedURL
	}

	updated, err := a.config.Update(map[string]any{
		"login_page_image_url":        nextImageURL,
		"login_page_image_mode":       strings.TrimSpace(r.FormValue("login_page_image_mode")),
		"login_page_image_zoom":       strings.TrimSpace(r.FormValue("login_page_image_zoom")),
		"login_page_image_position_x": strings.TrimSpace(r.FormValue("login_page_image_position_x")),
		"login_page_image_position_y": strings.TrimSpace(r.FormValue("login_page_image_position_y")),
	})
	if err != nil {
		if uploadedImageURL != "" {
			a.deleteLocalLoginPageImage(uploadedImageURL)
		}
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if currentImageURL != "" && currentImageURL != nextImageURL {
		a.deleteLocalLoginPageImage(currentImageURL)
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"config": updated})
}

func (a *App) storeLoginPageImage(header *multipart.FileHeader) (string, error) {
	data, ext, err := readLoginPageImageFile(header)
	if err != nil {
		return "", err
	}
	stem := safeUploadStem(header.Filename)
	if stem == "" {
		stem = "login-page"
	}
	filename := fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), stem, ext)
	target := filepath.Join(a.config.LoginPageImagesDir(), filename)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", err
	}
	return "/login-page-images/" + filename, nil
}

func readLoginPageImageFile(header *multipart.FileHeader) ([]byte, string, error) {
	if header == nil {
		return nil, "", fmt.Errorf("image file is required")
	}
	if header.Size > maxLoginPageImageSize {
		return nil, "", fmt.Errorf("login page image cannot exceed 10MB")
	}
	file, err := header.Open()
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLoginPageImageSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("image file is empty")
	}
	if len(data) > maxLoginPageImageSize {
		return nil, "", fmt.Errorf("login page image cannot exceed 10MB")
	}
	if ext := strings.ToLower(filepath.Ext(header.Filename)); ext == ".svg" && bytes.Contains(bytes.ToLower(data[:min(len(data), 512)]), []byte("<svg")) {
		return data, ".svg", nil
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return nil, "", fmt.Errorf("unsupported image file")
	}
	switch http.DetectContentType(data) {
	case "image/jpeg":
		return data, ".jpg", nil
	case "image/gif":
		return data, ".gif", nil
	case "image/webp":
		return data, ".webp", nil
	default:
		return data, ".png", nil
	}
}

func (a *App) deleteLocalLoginPageImage(imageURL string) {
	imagePath, ok := a.localLoginPageImagePath(imageURL)
	if ok {
		_ = os.Remove(imagePath)
	}
}

func (a *App) localLoginPageImagePath(imageURL string) (string, bool) {
	cleanURL := strings.TrimSpace(imageURL)
	if !strings.HasPrefix(cleanURL, "/login-page-images/") {
		return "", false
	}
	rel := strings.TrimPrefix(path.Clean(cleanURL), "/login-page-images/")
	if rel == "." || rel == "" || strings.Contains(rel, "..") {
		return "", false
	}
	root, err := filepath.Abs(a.config.LoginPageImagesDir())
	if err != nil {
		return "", false
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", false
	}
	return target, true
}

func firstMultipartFile(form *multipart.Form, key string) *multipart.FileHeader {
	if form == nil || len(form.File[key]) == 0 {
		return nil
	}
	return form.File[key][0]
}

func safeUploadStem(filename string) string {
	name := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	name = strings.ToLower(strings.TrimSpace(name))
	var builder strings.Builder
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-' || char == '_':
			builder.WriteRune(char)
		case char == ' ' || char == '.':
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-_")
}

func (a *App) handleImages(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	scope := imageAccessScope(identity)
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, a.images.ListImages(a.resolveImageBaseURL(r), strings.TrimSpace(r.URL.Query().Get("start_date")), strings.TrimSpace(r.URL.Query().Get("end_date")), scope))
	case http.MethodDelete:
		if identity.Role == service.AuthRoleUser && identity.Provider == service.AuthProviderLinuxDo {
			util.WriteError(w, http.StatusForbidden, "Linuxdo users cannot delete images")
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		result, err := a.images.DeleteImages(util.AsStringSlice(body["paths"]), scope)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, result)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleImageThumbnail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	thumbnailRel, err := imageThumbnailRequestPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sourceRel, sourceErr := a.images.SourceImageRelativePathFromThumbnail(thumbnailRel)
	if sourceErr != nil {
		http.NotFound(w, r)
		return
	}
	_ = a.images.EnsureThumbnail(thumbnailRel)
	thumbPath := filepath.Join(a.config.ImageThumbnailsDir(), filepath.FromSlash(thumbnailRel))
	if info, err := os.Stat(thumbPath); err == nil && !info.IsDir() {
		http.ServeFile(w, r, thumbPath)
		return
	}
	sourcePath := filepath.Join(a.config.ImagesDir(), filepath.FromSlash(sourceRel))
	if info, err := os.Stat(sourcePath); err == nil && !info.IsDir() {
		http.ServeFile(w, r, sourcePath)
		return
	}
	http.NotFound(w, r)
}

func imageThumbnailRequestPath(r *http.Request) (string, error) {
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/image-thumbnails/")
	if raw == "" || raw == r.URL.EscapedPath() {
		return "", errors.New("invalid thumbnail path")
	}
	rel, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	return rel, nil
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.logs.List(strings.TrimSpace(r.URL.Query().Get("type")), strings.TrimSpace(r.URL.Query().Get("start_date")), strings.TrimSpace(r.URL.Query().Get("end_date")), 200)})
}

func (a *App) handleStorageInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	backend, err := a.config.StorageBackend()
	if err != nil {
		util.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"backend": backend.Info(), "health": backend.HealthCheck()})
}

func (a *App) handleProxy(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if r.URL.Path == "/api/proxy/test" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := readJSONMap(r)
		candidate := strings.TrimSpace(util.Clean(body["url"]))
		if candidate == "" {
			candidate = a.config.Proxy()
		}
		if candidate == "" {
			util.WriteError(w, http.StatusBadRequest, "proxy url is required")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"result": a.proxy.Test(candidate, 15*time.Second)})
		return
	}
	switch r.Method {
	case http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"proxy": map[string]any{"url": a.config.Proxy()}})
	case http.MethodPost:
		body, _ := readJSONMap(r)
		url := util.Clean(body["url"])
		updated, err := a.config.Update(map[string]any{"proxy": url})
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"proxy": map[string]any{"url": updated["proxy"]}})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) requireIdentity(w http.ResponseWriter, r *http.Request, overrideAuth string) (service.Identity, bool) {
	auth := overrideAuth
	if auth == "" {
		auth = r.Header.Get("Authorization")
	}
	token := extractBearerToken(auth)
	if token != "" && token == a.config.AuthKey() {
		return service.Identity{ID: "admin", Name: "管理员", Role: service.AuthRoleAdmin, Provider: service.AuthProviderLocal, Kind: service.AuthKindSession}, true
	}
	if identity := a.auth.Authenticate(token); identity != nil {
		return *identity, true
	}
	util.WriteError(w, http.StatusUnauthorized, "authorization is invalid")
	return service.Identity{}, false
}

func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) (service.Identity, bool) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return service.Identity{}, false
	}
	if identity.Role != "admin" {
		util.WriteError(w, http.StatusForbidden, "admin permission required")
		return service.Identity{}, false
	}
	return identity, true
}

func extractBearerToken(auth string) string {
	scheme, value, ok := strings.Cut(strings.TrimSpace(auth), " ")
	if !ok || strings.ToLower(scheme) != "bearer" {
		return ""
	}
	return strings.TrimSpace(value)
}

func (a *App) resolveImageBaseURL(r *http.Request) string {
	if base := a.config.BaseURL(); base != "" {
		return base
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("x-forwarded-proto"); forwarded != "" {
		scheme = strings.Split(forwarded, ",")[0]
	}
	host := r.Host
	if value := r.Header.Get("host"); value != "" {
		host = value
	}
	return scheme + "://" + host
}

func readJSONMap(r *http.Request) (map[string]any, error) {
	var body map[string]any
	err := util.DecodeJSON(r.Body, &body)
	if body == nil {
		body = map[string]any{}
	}
	return body, err
}

func readMultipartImageBody(r *http.Request) (map[string]any, []protocol.UploadedImage, error) {
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		return nil, nil, err
	}
	body := map[string]any{
		"client_task_id":  firstForm(r.MultipartForm, "client_task_id"),
		"prompt":          firstForm(r.MultipartForm, "prompt"),
		"model":           firstNonEmpty(firstForm(r.MultipartForm, "model"), util.ImageModelAuto),
		"n":               util.ToInt(firstForm(r.MultipartForm, "n"), 1),
		"size":            firstForm(r.MultipartForm, "size"),
		"response_format": firstNonEmpty(firstForm(r.MultipartForm, "response_format"), "b64_json"),
		"stream":          util.ToBool(firstForm(r.MultipartForm, "stream")),
	}
	if rawMessages := strings.TrimSpace(firstForm(r.MultipartForm, "messages")); rawMessages != "" {
		var messages any
		if err := json.Unmarshal([]byte(rawMessages), &messages); err != nil {
			return nil, nil, fmt.Errorf("invalid messages")
		}
		body["messages"] = messages
	}
	var images []protocol.UploadedImage
	for _, field := range []string{"image", "image[]"} {
		for _, header := range r.MultipartForm.File[field] {
			image, err := readUpload(header)
			if err != nil {
				return nil, nil, err
			}
			if len(image.Data) == 0 {
				return nil, nil, fmt.Errorf("image file is empty")
			}
			images = append(images, image)
		}
	}
	return body, images, nil
}

func firstForm(form *multipart.Form, key string) string {
	if form == nil || len(form.Value[key]) == 0 {
		return ""
	}
	return form.Value[key][0]
}

func readUpload(header *multipart.FileHeader) (protocol.UploadedImage, error) {
	file, err := header.Open()
	if err != nil {
		return protocol.UploadedImage{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return protocol.UploadedImage{}, err
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/png"
	}
	filename := header.Filename
	if filename == "" {
		filename = "image.png"
	}
	return protocol.UploadedImage{Data: data, Filename: filename, ContentType: contentType}, nil
}

func jsonString(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func openAIErrorForStream(err error) map[string]any {
	var imageErr *protocol.ImageGenerationError
	if errors.As(err, &imageErr) {
		return imageErr.OpenAIError()
	}
	return map[string]any{"error": map[string]any{"message": err.Error(), "type": fmt.Sprintf("%T", err)}}
}

func (a *App) logCall(identity service.Identity, summary, endpoint, model string, started time.Time, status, errText string, urls []string) {
	detail := map[string]any{"endpoint": endpoint, "model": model, "started_at": started.Format("2006-01-02 15:04:05"), "ended_at": time.Now().Format("2006-01-02 15:04:05"), "duration_ms": time.Since(started).Milliseconds(), "status": status}
	addIdentityLogDetail(detail, identity)
	if errText != "" {
		detail["error"] = errText
	}
	if len(urls) > 0 {
		detail["urls"] = dedupe(urls)
	}
	suffix := "调用完成"
	if status == "failed" {
		suffix = "调用失败"
	}
	a.logs.Add(service.LogTypeCall, summary+suffix, detail)
}

func addIdentityLogDetail(detail map[string]any, identity service.Identity) {
	if name := util.Clean(firstNonEmpty(identity.CredentialName, identity.Name)); name != "" {
		detail["key_name"] = name
	}
	if role := util.Clean(identity.Role); role != "" {
		detail["key_role"] = role
	}
	if id := util.Clean(firstNonEmpty(identity.CredentialID, identity.ID)); id != "" {
		detail["key_id"] = id
	}
	if id := util.Clean(identity.ID); id != "" && id != util.Clean(identity.CredentialID) {
		detail["subject_id"] = id
	}
	if provider := util.Clean(identity.Provider); provider != "" {
		detail["provider"] = provider
	}
}

func identityScope(identity service.Identity) string {
	if owner := util.Clean(identity.OwnerID); owner != "" {
		return owner
	}
	if id := util.Clean(identity.ID); id != "" {
		return id
	}
	return "anonymous"
}

func imageAccessScope(identity service.Identity) service.ImageAccessScope {
	if identity.Role == service.AuthRoleAdmin {
		return service.ImageAccessScope{All: true}
	}
	return service.ImageAccessScope{OwnerID: identityScope(identity)}
}

func (a *App) recordImageOwners(identity service.Identity, urls []string) {
	if len(urls) == 0 || a.images == nil {
		return
	}
	a.images.EnsureThumbnails(urls)
	ownerID := identityScope(identity)
	if ownerID == "" || ownerID == "anonymous" {
		return
	}
	a.images.RecordImageOwners(urls, ownerID)
}

func (a *App) runLoggedImageTask(ctx context.Context, identity service.Identity, payload map[string]any, endpoint, summary string, run func(context.Context, map[string]any) (map[string]any, error)) (map[string]any, error) {
	start := time.Now()
	payload["owner_id"] = identityScope(identity)
	model := firstNonEmpty(util.Clean(payload["model"]), util.ImageModelAuto)
	result, err := run(ctx, payload)
	urls := collectURLs(result)
	a.recordImageOwners(identity, urls)
	if err != nil {
		a.logCall(identity, summary, endpoint, model, start, "failed", err.Error(), urls)
		return result, err
	}
	if len(util.AsMapSlice(result["data"])) == 0 {
		message := firstNonEmpty(util.Clean(result["message"]), "image task returned no image data")
		a.logCall(identity, summary, endpoint, model, start, "failed", message, urls)
		return result, nil
	}
	a.logCall(identity, summary, endpoint, model, start, "success", "", urls)
	return result, nil
}

func collectURLs(v any) []string {
	switch x := v.(type) {
	case map[string]any:
		var urls []string
		for key, value := range x {
			if key == "url" {
				if u := util.Clean(value); u != "" {
					urls = append(urls, u)
				}
			} else if key == "urls" {
				for _, raw := range anyList(value) {
					if u := util.Clean(raw); u != "" {
						urls = append(urls, u)
					}
				}
			} else {
				urls = append(urls, collectURLs(value)...)
			}
		}
		return urls
	case []any:
		var urls []string
		for _, item := range x {
			urls = append(urls, collectURLs(item)...)
		}
		return urls
	case []map[string]any:
		var urls []string
		for _, item := range x {
			urls = append(urls, collectURLs(item)...)
		}
		return urls
	default:
		return nil
	}
}

func dedupe(items []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func anyList(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	if list, ok := v.([]map[string]any); ok {
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = item
		}
		return out
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a *App) serveWeb(w http.ResponseWriter, r *http.Request) {
	webDist := filepath.Join(a.config.RootDir, "web_dist")
	clean := strings.Trim(r.URL.Path, "/")
	if asset := resolveWebAsset(webDist, clean); asset != "" {
		http.ServeFile(w, r, asset)
		return
	}
	last := clean
	if idx := strings.LastIndex(last, "/"); idx >= 0 {
		last = last[idx+1:]
	}
	if strings.HasPrefix(clean, "assets/") || strings.Contains(last, ".") {
		http.NotFound(w, r)
		return
	}
	if asset := resolveWebAsset(webDist, ""); asset != "" {
		http.ServeFile(w, r, asset)
		return
	}
	http.NotFound(w, r)
}

func resolveWebAsset(webDist, requested string) string {
	if info, err := os.Stat(webDist); err != nil || !info.IsDir() {
		return ""
	}
	base, _ := filepath.Abs(webDist)
	var candidates []string
	if requested == "" {
		candidates = []string{filepath.Join(base, "index.html")}
	} else {
		candidates = []string{filepath.Join(base, filepath.FromSlash(requested)), filepath.Join(base, filepath.FromSlash(requested), "index.html"), filepath.Join(base, filepath.FromSlash(requested)+".html")}
	}
	for _, candidate := range candidates {
		resolved, _ := filepath.Abs(candidate)
		if !strings.HasPrefix(resolved, base) {
			continue
		}
		if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
			return resolved
		}
	}
	return ""
}
