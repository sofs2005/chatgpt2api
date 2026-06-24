package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"chatgpt2api/internal/util"
)

func (a *App) handleEditableFileGenerations(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.editableFiles == nil {
		util.WriteError(w, http.StatusInternalServerError, "editable file task service is not configured")
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	a.globalLimiter.SetLimit(a.config.GlobalConcurrentLimit())
	release, err := a.globalLimiter.Acquire(r.Context())
	if err != nil {
		util.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer release()
	prompt := util.Clean(body["prompt"])
	kind := editableFileKindFromPath(r.URL.Path)
	if kind == "psd" && len(util.AsStringSlice(body["base64_images"])) == 0 {
		util.WriteError(w, http.StatusBadRequest, "base64_images is required")
		return
	}
	task, err := a.editableFiles.Submit(r.Context(), identity, kind, prompt, util.AsStringSlice(body["base64_images"]), util.Clean(body["client_task_id"]))
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	util.WriteJSON(w, http.StatusOK, task)
}

func (a *App) handleEditableFileTasks(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.editableFiles == nil {
		util.WriteError(w, http.StatusInternalServerError, "editable file task service is not configured")
		return
	}
	util.WriteJSON(w, http.StatusOK, a.editableFiles.List(identity, util.ParseCommaList(r.URL.Query().Get("ids"))))
}

func (a *App) handlePublicEditableFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.editableFiles == nil {
		http.NotFound(w, r)
		return
	}
	rel, err := editableFileRequestPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	abs, err := a.editableFiles.PublicFilePath(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, abs)
}

func editableFileKindFromPath(path string) string {
	switch strings.TrimSpace(path) {
	case "/v1/psd/generations":
		return "psd"
	default:
		return "ppt"
	}
}

func editableFileRequestPath(r *http.Request) (string, error) {
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/files/")
	if raw == "" || raw == r.URL.EscapedPath() {
		return "", errors.New("invalid file path")
	}
	return url.PathUnescape(raw)
}
