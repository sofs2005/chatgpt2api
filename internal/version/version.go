// Package version exposes the application version injected at build time.
package version

import (
	"os"
	"strings"
)

// Version is overridden by builds with:
//
//	go build -tags=embed -ldflags "-X chatgpt2api/internal/version.Version=1.2.3"
var Version = "0.0.0-dev"

// Commit, Date, and BuildType are overridden by release builds.
var (
	Commit     = "unknown"
	Date       = "unknown"
	BuildType  = "source"
	Deployment = "binary"
)

type Info struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	Date       string `json:"date"`
	BuildType  string `json:"build_type"`
	Deployment string `json:"deployment"`
}

// Get returns the normalized application version.
func Get() string {
	if value := strings.TrimSpace(Version); value != "" {
		return value
	}
	return "0.0.0-dev"
}

func GetBuildType() string {
	if value := strings.TrimSpace(BuildType); value != "" {
		return value
	}
	return "source"
}

func GetDeployment() string {
	if value := strings.TrimSpace(os.Getenv("CHATGPT2API_DEPLOYMENT")); value != "" {
		return value
	}
	if value := strings.TrimSpace(Deployment); value != "" {
		return value
	}
	return "binary"
}

func GetInfo() Info {
	return Info{
		Version:    Get(),
		Commit:     strings.TrimSpace(Commit),
		Date:       strings.TrimSpace(Date),
		BuildType:  GetBuildType(),
		Deployment: GetDeployment(),
	}
}
