package service

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	updateCacheTTL       = 20 * time.Minute
	defaultUpdateRepo    = "ZyphrZero/chatgpt2api"
	defaultDockerImage   = "zyphrzero/chatgpt2api"
	defaultGitHubAPIBase = "https://api.github.com"
	defaultDockerHubBase = "https://hub.docker.com"
	maxUpdateDownload    = 500 * 1024 * 1024
)

type UpdateService struct {
	mu             sync.Mutex
	repo           string
	apiBaseURL     string
	dockerHubBase  string
	dockerImage    string
	githubToken    string
	currentVersion string
	buildType      string
	deployment     string
	httpClient     *http.Client
	downloadClient *http.Client
	cached         *UpdateInfo
	cachedAt       time.Time
}

type UpdateOptions struct {
	Repo           string
	APIBaseURL     string
	DockerHubBase  string
	DockerImage    string
	GitHubToken    string
	CurrentVersion string
	BuildType      string
	Deployment     string
	ProxyURL       string
}

type UpdateInfo struct {
	CurrentVersion string       `json:"current_version"`
	LatestVersion  string       `json:"latest_version"`
	HasUpdate      bool         `json:"has_update"`
	ReleaseInfo    *ReleaseInfo `json:"release_info,omitempty"`
	Cached         bool         `json:"cached"`
	Warning        string       `json:"warning,omitempty"`
	BuildType      string       `json:"build_type"`
	Deployment     string       `json:"deployment"`
	UpdateSource   string       `json:"update_source"`
}

type ReleaseInfo struct {
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt time.Time     `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type dockerTagResponse struct {
	Name        string           `json:"name"`
	Images      []dockerTagImage `json:"images"`
	Digest      string           `json:"digest"`
	LastUpdated time.Time        `json:"last_updated"`
}

type dockerTagsResponse struct {
	Results []dockerTagResponse `json:"results"`
}

type dockerTagImage struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Digest       string `json:"digest"`
}

func NewUpdateService(options UpdateOptions) *UpdateService {
	repo := strings.TrimSpace(options.Repo)
	if repo == "" {
		repo = defaultUpdateRepo
	}
	apiBaseURL := strings.TrimRight(strings.TrimSpace(options.APIBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = defaultGitHubAPIBase
	}
	dockerHubBase := strings.TrimRight(strings.TrimSpace(options.DockerHubBase), "/")
	if dockerHubBase == "" {
		dockerHubBase = defaultDockerHubBase
	}
	dockerImage := strings.Trim(strings.TrimSpace(options.DockerImage), "/")
	if dockerImage == "" {
		dockerImage = defaultDockerImage
	}
	buildType := strings.TrimSpace(options.BuildType)
	if buildType == "" {
		buildType = "source"
	}
	deployment := strings.TrimSpace(options.Deployment)
	if deployment == "" {
		deployment = "binary"
	}
	return &UpdateService{
		repo:           repo,
		apiBaseURL:     apiBaseURL,
		dockerHubBase:  dockerHubBase,
		dockerImage:    dockerImage,
		githubToken:    strings.TrimSpace(options.GitHubToken),
		currentVersion: strings.TrimSpace(options.CurrentVersion),
		buildType:      buildType,
		deployment:     deployment,
		httpClient:     HTTPClientForProxy(options.ProxyURL, 30*time.Second),
		downloadClient: HTTPClientForProxy(options.ProxyURL, 10*time.Minute),
	}
}

func (s *UpdateService) CheckUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	if !force {
		if cached := s.cachedInfo(); cached != nil {
			return cached, nil
		}
	}
	info, err := s.fetchLatestRelease(ctx)
	if err != nil {
		if cached := s.cachedInfo(); cached != nil {
			cached.Warning = "Using cached data: " + err.Error()
			return cached, nil
		}
		return &UpdateInfo{
			CurrentVersion: s.currentVersion,
			LatestVersion:  s.currentVersion,
			HasUpdate:      false,
			Warning:        err.Error(),
			BuildType:      s.buildType,
			Deployment:     s.deployment,
			UpdateSource:   s.updateSource(),
		}, nil
	}
	s.saveInfo(info)
	return cloneUpdateInfo(info), nil
}

func (s *UpdateService) PerformUpdate(ctx context.Context) error {
	if s.isDockerDeployment() {
		return errors.New("Docker deployment updates are handled by pulling the DockerHub image")
	}
	info, err := s.CheckUpdate(ctx, true)
	if err != nil {
		return err
	}
	if !info.HasUpdate {
		return errors.New("no update available")
	}
	if info.ReleaseInfo == nil {
		return errors.New("release info is missing")
	}

	archiveName := runtime.GOOS + "_" + runtime.GOARCH
	downloadURL, checksumURL := matchingReleaseAssets(info.ReleaseInfo.Assets, archiveName)
	if downloadURL == "" {
		return fmt.Errorf("no compatible release archive found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if err := validateUpdateDownloadURL(downloadURL); err != nil {
		return fmt.Errorf("invalid download URL: %w", err)
	}
	if checksumURL != "" {
		if err := validateUpdateDownloadURL(checksumURL); err != nil {
			return fmt.Errorf("invalid checksum URL: %w", err)
		}
	}

	exePath, err := executablePath()
	if err != nil {
		return err
	}
	exeDir := filepath.Dir(exePath)
	tempDir, err := os.MkdirTemp(exeDir, ".chatgpt2api-update-*")
	if err != nil {
		return fmt.Errorf("create update temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	archivePath := filepath.Join(tempDir, downloadFileName(downloadURL))
	if err := s.downloadFile(ctx, downloadURL, archivePath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	if checksumURL != "" {
		if err := s.verifyChecksum(ctx, archivePath, checksumURL); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	extractDir := filepath.Join(tempDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	if err := extractUpdateArchive(archivePath, extractDir); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	newBinaryPath, err := findExtractedBinary(extractDir)
	if err != nil {
		return err
	}
	if err := os.Chmod(newBinaryPath, 0o755); err != nil {
		return fmt.Errorf("chmod updated binary: %w", err)
	}

	return replaceRuntimeFiles(exePath, newBinaryPath)
}

func (s *UpdateService) Rollback() error {
	exePath, err := executablePath()
	if err != nil {
		return err
	}
	return rollbackRuntimeFiles(exePath)
}

func (s *UpdateService) fetchLatestRelease(ctx context.Context) (*UpdateInfo, error) {
	if s.isDockerDeployment() {
		return s.fetchDockerHubTag(ctx)
	}
	apiURL := s.apiBaseURL + "/repos/" + strings.Trim(s.repo, "/") + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "chatgpt2api-updater")
	if s.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.githubToken)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, githubAPIStatusError(resp, s.repo)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	latestVersion := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	assets := make([]Asset, 0, len(release.Assets))
	for _, asset := range release.Assets {
		assets = append(assets, Asset{
			Name:        asset.Name,
			DownloadURL: asset.BrowserDownloadURL,
			Size:        asset.Size,
		})
	}
	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  latestVersion,
		HasUpdate:      compareVersions(s.currentVersion, latestVersion) < 0,
		ReleaseInfo: &ReleaseInfo{
			Name:        release.Name,
			Body:        release.Body,
			PublishedAt: release.PublishedAt,
			HTMLURL:     release.HTMLURL,
			Assets:      assets,
		},
		Cached:       false,
		BuildType:    s.buildType,
		Deployment:   s.deployment,
		UpdateSource: s.updateSource(),
	}, nil
}

func (s *UpdateService) fetchDockerHubTag(ctx context.Context) (*UpdateInfo, error) {
	image, ok := dockerHubImagePath(s.dockerImage)
	if !ok {
		return nil, fmt.Errorf("invalid DockerHub image: %s", s.dockerImage)
	}
	apiURL := s.dockerHubBase + "/v2/repositories/" + image + "/tags?page_size=100"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "chatgpt2api-updater")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DockerHub API returned %d for %s: %s", resp.StatusCode, s.dockerImage, dockerHubErrorMessage(resp.Body))
	}
	var tags dockerTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	latestTag, ok := latestDockerVersionTag(tags.Results, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return nil, fmt.Errorf("DockerHub image %s has no version tag for %s/%s", s.dockerImage, runtime.GOOS, runtime.GOARCH)
	}
	latest := strings.TrimPrefix(strings.TrimSpace(latestTag.Name), "v")
	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  latest,
		HasUpdate:      latest != s.currentVersion && compareVersions(s.currentVersion, latest) < 0,
		Cached:         false,
		BuildType:      s.buildType,
		Deployment:     s.deployment,
		UpdateSource:   s.updateSource(),
		ReleaseInfo: &ReleaseInfo{
			Name:        s.dockerImage + ":latest",
			PublishedAt: latestTag.LastUpdated,
			HTMLURL:     "https://hub.docker.com/r/" + image + "/tags",
		},
	}, nil
}

func (s *UpdateService) isDockerDeployment() bool {
	return strings.EqualFold(strings.TrimSpace(s.deployment), "docker")
}

func (s *UpdateService) updateSource() string {
	if s.isDockerDeployment() {
		return "dockerhub"
	}
	return "github-release"
}

func dockerHubImagePath(image string) (string, bool) {
	image = strings.Trim(strings.TrimSpace(image), "/")
	if image == "" || strings.Contains(image, "://") {
		return "", false
	}
	parts := strings.Split(image, "/")
	if len(parts) == 1 {
		image = "library/" + parts[0]
		parts = strings.Split(image, "/")
	}
	if len(parts) != 2 {
		return "", false
	}
	for _, part := range parts {
		if part == "" || strings.ContainsAny(part, " \t\r\n:") {
			return "", false
		}
	}
	return image, true
}

func latestDockerVersionTag(tags []dockerTagResponse, goos, goarch string) (dockerTagResponse, bool) {
	var latest dockerTagResponse
	for _, tag := range tags {
		name := strings.TrimSpace(tag.Name)
		if !isDockerVersionTag(name) || !dockerTagSupportsRuntime(tag.Images, goos, goarch) {
			continue
		}
		if latest.Name == "" || compareVersions(latest.Name, name) < 0 {
			latest = tag
		}
	}
	return latest, latest.Name != ""
}

func isDockerVersionTag(name string) bool {
	name = strings.TrimPrefix(strings.TrimSpace(name), "v")
	if name == "" || strings.Contains(name, "-") {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func dockerTagSupportsRuntime(images []dockerTagImage, goos, goarch string) bool {
	if len(images) == 0 {
		return true
	}
	for _, image := range images {
		if strings.EqualFold(image.OS, goos) && strings.EqualFold(image.Architecture, goarch) {
			return true
		}
	}
	return false
}

func dockerHubErrorMessage(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, 64*1024))
	var payload struct {
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(data, &payload); err == nil {
		if message := strings.TrimSpace(payload.Message); message != "" {
			return message
		}
		if detail := strings.TrimSpace(payload.Detail); detail != "" {
			return detail
		}
	}
	return strings.TrimSpace(string(data))
}

func githubAPIStatusError(resp *http.Response, repo string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	message := githubErrorMessage(body)
	parts := []string{fmt.Sprintf("GitHub API returned %d", resp.StatusCode)}
	if message != "" {
		parts = append(parts, message)
	}
	if resp.StatusCode == http.StatusNotFound {
		parts = append(parts, fmt.Sprintf("latest GitHub Release was not found for %s; publish a GitHub Release with release archives, configure CHATGPT2API_UPDATE_REPO to the repository that contains releases, or ensure the GitHub token can read the repository", repo))
	}
	if resp.StatusCode == http.StatusForbidden && strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining")) == "0" {
		hint := "GitHub API rate limit exhausted"
		if reset := githubRateLimitReset(resp.Header.Get("X-RateLimit-Reset")); reset != "" {
			hint += "; reset at " + reset
		}
		hint += "; set CHATGPT2API_UPDATE_GITHUB_TOKEN to use authenticated GitHub API requests"
		parts = append(parts, hint)
	}
	return errors.New(strings.Join(parts, ": "))
}

func githubErrorMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		return strings.TrimSpace(payload.Message)
	}
	return strings.TrimSpace(string(body))
}

func githubRateLimitReset(value string) string {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).Format(time.RFC3339)
}

func (s *UpdateService) downloadFile(ctx context.Context, downloadURL, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.downloadClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	if resp.ContentLength > maxUpdateDownload {
		return fmt.Errorf("download too large: %d bytes", resp.ContentLength)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxUpdateDownload+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	if written > maxUpdateDownload {
		_ = os.Remove(dest)
		return fmt.Errorf("download exceeded maximum size of %d bytes", maxUpdateDownload)
	}
	return nil
}

func (s *UpdateService) verifyChecksum(ctx context.Context, filePath, checksumURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksum download returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return err
	}
	hash, err := fileSHA256(filePath)
	if err != nil {
		return err
	}
	fileName := filepath.Base(filePath)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 2 && strings.TrimPrefix(parts[1], "*") == fileName {
			if strings.EqualFold(parts[0], hash) {
				return nil
			}
			return fmt.Errorf("checksum mismatch: expected %s, got %s", parts[0], hash)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("checksum not found for %s", fileName)
}

func (s *UpdateService) cachedInfo() *UpdateInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached == nil || time.Since(s.cachedAt) > updateCacheTTL {
		return nil
	}
	out := cloneUpdateInfo(s.cached)
	out.Cached = true
	return out
}

func (s *UpdateService) saveInfo(info *UpdateInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = cloneUpdateInfo(info)
	s.cachedAt = time.Now()
}

func matchingReleaseAssets(assets []Asset, archiveName string) (string, string) {
	var downloadURL string
	var checksumURL string
	for _, asset := range assets {
		name := strings.TrimSpace(asset.Name)
		if name == "checksums.txt" {
			checksumURL = asset.DownloadURL
			continue
		}
		if strings.Contains(name, archiveName) && !strings.HasSuffix(name, ".txt") {
			downloadURL = asset.DownloadURL
		}
	}
	return downloadURL, checksumURL
}

func validateUpdateDownloadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return errors.New("only HTTPS URLs are allowed")
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case host == "github.com":
		return nil
	case strings.HasSuffix(host, ".github.com"):
		return nil
	case host == "objects.githubusercontent.com":
		return nil
	case strings.HasSuffix(host, ".objects.githubusercontent.com"):
		return nil
	default:
		return fmt.Errorf("download from untrusted host: %s", host)
	}
}

func executablePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return resolved, nil
}

func downloadFileName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil {
		if name := path.Base(parsed.Path); name != "." && name != "/" {
			return name
		}
	}
	return "update-archive"
}

func extractUpdateArchive(archivePath, destDir string) error {
	switch {
	case strings.HasSuffix(archivePath, ".zip"):
		return extractZipArchive(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".tar.gz"), strings.HasSuffix(archivePath, ".tgz"), strings.HasSuffix(archivePath, ".gz"):
		return extractTarGzipArchive(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".tar"):
		return extractTarArchiveFile(archivePath, destDir)
	default:
		return fmt.Errorf("unsupported archive format: %s", filepath.Base(archivePath))
	}
}

func extractTarGzipArchive(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()
	return extractTar(gzr, destDir)
}

func extractTarArchiveFile(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	return extractTar(f, destDir)
}

func extractTar(reader io.Reader, destDir string) error {
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeExtractPath(destDir, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size > maxUpdateDownload {
				return fmt.Errorf("archive entry too large: %s", header.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, io.LimitReader(tr, maxUpdateDownload+1))
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func extractZipArchive(archivePath, destDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		target, err := safeExtractPath(destDir, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if file.UncompressedSize64 > uint64(maxUpdateDownload) {
			return fmt.Errorf("archive entry too large: %s", file.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(in, maxUpdateDownload+1))
		closeInErr := in.Close()
		closeOutErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInErr != nil {
			return closeInErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
	}
	return nil
}

func safeExtractPath(destDir, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if cleanName == "." || cleanName == string(filepath.Separator) {
		return "", fmt.Errorf("invalid archive path: %q", name)
	}
	if filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path: %q", name)
	}
	target := filepath.Join(destDir, cleanName)
	base, err := filepath.Abs(destDir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if resolved != base && !strings.HasPrefix(resolved, base+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path: %q", name)
	}
	return resolved, nil
}

func findExtractedBinary(root string) (string, error) {
	name := "chatgpt2api"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("release archive does not contain %s", name)
	}
	return found, nil
}

func replaceRuntimeFiles(exePath, newBinaryPath string) error {
	exeBackup := exePath + ".backup"
	_ = os.Remove(exeBackup)
	if err := os.Rename(exePath, exeBackup); err != nil {
		return fmt.Errorf("backup executable: %w", err)
	}
	binaryReplaced := false
	defer func() {
		if !binaryReplaced {
			_ = os.Rename(exeBackup, exePath)
		}
	}()
	if err := os.Rename(newBinaryPath, exePath); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	binaryReplaced = true
	return nil
}

func rollbackRuntimeFiles(exePath string) error {
	exeBackup := exePath + ".backup"
	if _, err := os.Stat(exeBackup); err != nil {
		if os.IsNotExist(err) {
			return errors.New("no executable backup found")
		}
		return err
	}
	currentBackup := exePath + ".rollback-current"
	_ = os.Remove(currentBackup)
	if err := os.Rename(exePath, currentBackup); err != nil {
		return fmt.Errorf("backup current executable: %w", err)
	}
	if err := os.Rename(exeBackup, exePath); err != nil {
		_ = os.Rename(currentBackup, exePath)
		return fmt.Errorf("restore executable backup: %w", err)
	}
	_ = os.Remove(currentBackup)
	return nil
}

func fileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func compareVersions(current, latest string) int {
	currentParts := versionParts(current)
	latestParts := versionParts(latest)
	for i := 0; i < len(currentParts) || i < len(latestParts); i++ {
		var currentPart, latestPart int
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if currentPart < latestPart {
			return -1
		}
		if currentPart > latestPart {
			return 1
		}
	}
	return 0
}

func versionParts(value string) []int {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if idx := strings.IndexAny(value, "-+"); idx >= 0 {
		value = value[:idx]
	}
	fields := strings.Split(value, ".")
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		if len(out) == 3 {
			break
		}
		part, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || part < 0 {
			part = 0
		}
		out = append(out, part)
	}
	for len(out) < 3 {
		out = append(out, 0)
	}
	return out
}

func cloneUpdateInfo(info *UpdateInfo) *UpdateInfo {
	if info == nil {
		return nil
	}
	out := *info
	if info.ReleaseInfo != nil {
		release := *info.ReleaseInfo
		release.Assets = append([]Asset(nil), info.ReleaseInfo.Assets...)
		out.ReleaseInfo = &release
	}
	return &out
}
