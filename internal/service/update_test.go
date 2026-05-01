package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current string
		latest  string
		want    int
	}{
		{name: "older patch", current: "1.2.3", latest: "1.2.4", want: -1},
		{name: "older minor", current: "1.2.9", latest: "1.3.0", want: -1},
		{name: "same with v prefix", current: "v1.2.3", latest: "1.2.3", want: 0},
		{name: "newer", current: "2.0.0", latest: "1.9.9", want: 1},
		{name: "pre release trims suffix", current: "1.2.3-dev", latest: "1.2.3", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := compareVersions(tc.current, tc.latest)
			if got != tc.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestValidateUpdateDownloadURL(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/ZyphrZero/chatgpt2api/releases/download/v1.0.0/chatgpt2api.tar.gz",
		"https://objects.githubusercontent.com/github-production-release-asset/example",
	} {
		if err := validateUpdateDownloadURL(raw); err != nil {
			t.Fatalf("validateUpdateDownloadURL(%q) error = %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://github.com/ZyphrZero/chatgpt2api/releases/download/v1.0.0/chatgpt2api.tar.gz",
		"https://example.com/chatgpt2api.tar.gz",
	} {
		if err := validateUpdateDownloadURL(raw); err == nil {
			t.Fatalf("validateUpdateDownloadURL(%q) succeeded, want error", raw)
		}
	}
}

func TestExtractUpdateArchiveFindsEmbeddedRuntimePayload(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "chatgpt2api_1.2.3_linux_amd64.tar.gz")
	if err := writeTestUpdateArchive(archivePath); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	extractDir := filepath.Join(root, "extract")
	if err := extractUpdateArchive(archivePath, extractDir); err != nil {
		t.Fatalf("extractUpdateArchive() error = %v", err)
	}
	wantBinaryName := "chatgpt2api"
	if runtime.GOOS == "windows" {
		wantBinaryName += ".exe"
	}
	if binary, err := findExtractedBinary(extractDir); err != nil || filepath.Base(binary) != wantBinaryName {
		t.Fatalf("findExtractedBinary() = %q, %v", binary, err)
	}
}

func TestGoReleaserArchiveDoesNotShipWebDist(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	config := string(data)
	if strings.Contains(config, "web_dist") {
		t.Fatal(".goreleaser.yaml must not ship runtime web_dist assets")
	}
	if !strings.Contains(config, "-tags=embed") {
		t.Fatal(".goreleaser.yaml must build the binary with embedded frontend assets")
	}
}

func TestGoReleaserBuildTargetsLinuxOnly(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	config := string(data)
	if !yamlListContains(config, "linux") {
		t.Fatal(".goreleaser.yaml build targets must include linux")
	}
	for _, entry := range []string{"windows", "darwin"} {
		if yamlListContains(config, entry) {
			t.Fatalf(".goreleaser.yaml build targets must not include %s", entry)
		}
	}
	if strings.Contains(config, "format_overrides:") {
		t.Fatal(".goreleaser.yaml must not keep non-Linux archive format overrides")
	}
}

func yamlListContains(config, value string) bool {
	for _, line := range strings.Split(config, "\n") {
		switch strings.TrimSpace(line) {
		case "- " + value, `- "` + value + `"`, "- '" + value + "'":
			return true
		}
	}
	return false
}

func TestSafeExtractPathRejectsTraversal(t *testing.T) {
	if _, err := safeExtractPath(t.TempDir(), "../outside"); err == nil {
		t.Fatal("safeExtractPath accepted traversal path")
	}
}

func TestDownloadFileNameIgnoresQuery(t *testing.T) {
	raw := "https://github.com/ZyphrZero/chatgpt2api/releases/download/v1.0.0/chatgpt2api.tar.gz?download=1"
	if got := downloadFileName(raw); got != "chatgpt2api.tar.gz" {
		t.Fatalf("downloadFileName(%q) = %q", raw, got)
	}
	if _, err := url.Parse(raw); err != nil {
		t.Fatalf("test URL invalid: %v", err)
	}
}

func TestFetchLatestReleaseUsesGitHubToken(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ghp_test_token" {
			t.Fatalf("Authorization header = %q, want bearer token", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got == "" {
			t.Fatal("missing GitHub API version header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.2.0",
			"name": "v1.2.0",
			"body": "release notes",
			"html_url": "https://github.com/ZyphrZero/chatgpt2api/releases/tag/v1.2.0",
			"published_at": "2026-01-01T00:00:00Z",
			"assets": []
		}`))
	}))
	defer api.Close()

	service := NewUpdateService(UpdateOptions{
		APIBaseURL:     api.URL,
		GitHubToken:    " ghp_test_token ",
		CurrentVersion: "1.1.0",
		BuildType:      "release",
	})
	info, err := service.fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestRelease() error = %v", err)
	}
	if info.LatestVersion != "1.2.0" || !info.HasUpdate {
		t.Fatalf("fetchLatestRelease() = %#v", info)
	}
}

func TestDockerDeploymentChecksDockerHubTags(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/repositories/zyphrzero/chatgpt2api/tags" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("page_size"); got != "100" {
			t.Fatalf("page_size = %q, want 100", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"name":"latest","images":[{"os":"linux","architecture":"amd64"},{"os":"linux","architecture":"arm64"}]},
				{"name":"0.1.3","last_updated":"2026-01-02T00:00:00Z","images":[{"os":"` + runtime.GOOS + `","architecture":"` + runtime.GOARCH + `"}]},
				{"name":"0.1.2","images":[{"os":"` + runtime.GOOS + `","architecture":"` + runtime.GOARCH + `"}]},
				{"name":"0.1.4-amd64","images":[{"os":"` + runtime.GOOS + `","architecture":"` + runtime.GOARCH + `"}]}
			]
		}`))
	}))
	defer api.Close()

	service := NewUpdateService(UpdateOptions{
		DockerHubBase:  api.URL,
		CurrentVersion: "0.1.2",
		BuildType:      "release",
		Deployment:     "docker",
	})
	info, err := service.fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestRelease() error = %v", err)
	}
	if info.LatestVersion != "0.1.3" || !info.HasUpdate || info.UpdateSource != "dockerhub" || info.Deployment != "docker" {
		t.Fatalf("fetchLatestRelease() = %#v", info)
	}
	if info.ReleaseInfo == nil || !strings.Contains(info.ReleaseInfo.HTMLURL, "hub.docker.com") {
		t.Fatalf("docker release info = %#v", info.ReleaseInfo)
	}
	if info.ReleaseInfo.PublishedAt.IsZero() {
		t.Fatalf("docker release info missing published time = %#v", info.ReleaseInfo)
	}
}

func TestDockerDeploymentDoesNotReplaceRuntimeBinary(t *testing.T) {
	service := NewUpdateService(UpdateOptions{Deployment: "docker"})
	err := service.PerformUpdate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "DockerHub image") {
		t.Fatalf("PerformUpdate() error = %v, want DockerHub image guidance", err)
	}
}

func TestGitHubRateLimitErrorIncludesActionableHint(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1777608736")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for 203.0.113.10."}`))
	}))
	defer api.Close()

	service := NewUpdateService(UpdateOptions{APIBaseURL: api.URL})
	_, err := service.fetchLatestRelease(context.Background())
	if err == nil {
		t.Fatal("fetchLatestRelease() succeeded, want rate limit error")
	}
	for _, want := range []string{
		"GitHub API returned 403",
		"API rate limit exceeded",
		"GitHub API rate limit exhausted",
		"CHATGPT2API_UPDATE_GITHUB_TOKEN",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestGitHubNotFoundErrorIncludesReleaseHint(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer api.Close()

	service := NewUpdateService(UpdateOptions{Repo: "owner/project", APIBaseURL: api.URL})
	_, err := service.fetchLatestRelease(context.Background())
	if err == nil {
		t.Fatal("fetchLatestRelease() succeeded, want not found error")
	}
	for _, want := range []string{
		"GitHub API returned 404",
		"latest GitHub Release was not found for owner/project",
		"CHATGPT2API_UPDATE_REPO",
		"GitHub token can read the repository",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func writeTestUpdateArchive(path string) error {
	binaryName := "chatgpt2api"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for name, content := range map[string]string{
		"chatgpt2api_1.2.3_linux_amd64/" + binaryName: "binary",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			return err
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			return err
		}
	}
	return nil
}
