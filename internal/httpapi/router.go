package httpapi

import (
	"net/http"
	"strings"

	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"
)

type routeMatch int

const (
	exactRoute routeMatch = iota
	prefixRoute
)

type appRoute struct {
	method  string
	path    string
	match   routeMatch
	handler http.HandlerFunc
}

func (a *App) Handler() http.Handler {
	routes := a.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.serveObservedHTTP(w, r, routes)
	})
}

func (a *App) routes() []appRoute {
	return []appRoute{
		exact(http.MethodGet, "/v1/models", a.handleModels),
		exact(http.MethodPost, "/v1/images/generations", a.handleImageGenerations),
		exact(http.MethodPost, "/v1/images/edits", a.handleImageEdits),
		exact(http.MethodPost, "/v1/chat/completions", a.handleChatCompletions),
		exact(http.MethodPost, "/v1/responses", a.handleResponses),
		exact(http.MethodPost, "/v1/messages", a.handleMessages),

		exact(http.MethodPost, "/auth/login", a.handleLogin),
		exact(http.MethodPost, "/auth/register", a.handleAccountRegister),
		exact(http.MethodGet, "/auth/session", a.handleSession),
		exact("", "/auth/providers", a.handleAuthProviders),
		exact("", "/auth/linuxdo/start", a.handleLinuxDoOAuthStart),
		exact("", "/auth/linuxdo/oauth/callback", a.handleLinuxDoOAuthCallback),
		exact(http.MethodGet, "/auth/linuxdo/callback", a.serveWeb),
		exact(http.MethodHead, "/auth/linuxdo/callback", a.serveWeb),
		exact(http.MethodGet, "/version", func(w http.ResponseWriter, _ *http.Request) {
			util.WriteJSON(w, http.StatusOK, map[string]any{"version": version.Get()})
		}),

		exact("", "/api/announcements", a.handlePublicAnnouncements),
		subtree("/api/admin/announcements", a.handleAdminAnnouncements),
		subtree("/api/admin/roles", a.handleAdminRoles),
		subtree("/api/admin/users", a.handleAdminUsers),
		exact("", "/api/profile", a.handleProfile),
		exact(http.MethodPost, "/api/profile/password", a.handleProfilePassword),
		subtree("/api/profile/api-key", a.handleProfileAPIKey),
		subtree("/api/auth/users", a.handleUserKeys),
		subtree("/api/accounts", a.handleAccounts),
		subtree("/api/cpa/pools", a.handleCPA),
		subtree("/api/sub2api/servers", a.handleSub2API),
		subtree("/api/creation-tasks", a.handleCreationTasks),
		subtree("/api/register", a.handleRegister),
		exact("", "/api/settings", a.handleSettings),
		exact("", "/api/settings/login-page-image", a.handleLoginPageImageSettings),
		exact(http.MethodGet, "/api/app-meta", a.handleAppMeta),
		exact(http.MethodGet, "/api/admin/permissions", a.handlePermissionCatalog),
		exact("", "/api/images/visibility", a.handleImageVisibility),
		exact("", "/api/images", a.handleImages),
		exact("", "/api/logs/governance", a.handleLogGovernance),
		exact(http.MethodGet, "/api/logs", a.handleLogs),
		exact("", "/api/proxy", a.handleProxy),
		exact("", "/api/proxy/test", a.handleProxy),
		exact(http.MethodGet, "/api/storage/info", a.handleStorageInfo),

		prefix("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir(a.config.ImagesDir()))).ServeHTTP),
		prefix("/image-thumbnails/", a.handleImageThumbnail),
		prefix("/login-page-images/", http.StripPrefix("/login-page-images/", http.FileServer(http.Dir(a.config.LoginPageImagesDir()))).ServeHTTP),
	}
}

func exact(method, path string, handler http.HandlerFunc) appRoute {
	return appRoute{method: method, path: path, match: exactRoute, handler: handler}
}

func prefix(path string, handler http.HandlerFunc) appRoute {
	return appRoute{path: path, match: prefixRoute, handler: handler}
}

func subtree(path string, handler http.HandlerFunc) appRoute {
	return prefix(path, handler)
}

func (a *App) serveHTTP(w http.ResponseWriter, r *http.Request, routes []appRoute) {
	applyCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if route := matchAppRoute(routes, r.Method, r.URL.Path); route != nil {
		route.handler(w, r)
		return
	}
	if isAPISpace(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	a.serveWeb(w, r)
}

func matchAppRoute(routes []appRoute, method, path string) *appRoute {
	for i := range routes {
		route := &routes[i]
		if route.method != "" && route.method != method {
			continue
		}
		switch route.match {
		case exactRoute:
			if path == route.path {
				return route
			}
		case prefixRoute:
			if path == route.path || strings.HasPrefix(path, strings.TrimRight(route.path, "/")+"/") {
				return route
			}
		}
	}
	return nil
}

func isAPISpace(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/auth" || strings.HasPrefix(path, "/auth/") ||
		path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func applyCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
}
