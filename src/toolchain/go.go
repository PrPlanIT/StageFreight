package toolchain

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// goDownloadURL returns the official Go release URL for a given version/platform.
func goDownloadURL(version, goos, goarch string) string {
	return fmt.Sprintf("https://go.dev/dl/go%s.%s-%s.tar.gz", version, goos, goarch)
}

// fetchGoChecksum retrieves the official SHA256 checksum for a Go release.
// Uses the Go downloads JSON API: https://go.dev/dl/?mode=json&include=all
func fetchGoChecksum(version, goos, goarch string) (string, error) {
	filename := fmt.Sprintf("go%s.%s-%s.tar.gz", version, goos, goarch)

	resp, err := httpGet("https://go.dev/dl/?mode=json&include=all")
	if err != nil {
		return "", fmt.Errorf("fetching Go release index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Go release index: HTTP %d", resp.StatusCode)
	}

	var releases []struct {
		Version string `json:"version"`
		Files   []struct {
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		} `json:"files"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("parsing Go release index: %w", err)
	}

	targetVersion := "go" + version
	for _, rel := range releases {
		if rel.Version != targetVersion {
			continue
		}
		for _, f := range rel.Files {
			if f.Filename == filename {
				if f.SHA256 == "" {
					return "", fmt.Errorf("Go %s: no SHA256 in release index for %s", version, filename)
				}
				return f.SHA256, nil
			}
		}
		return "", fmt.Errorf("Go %s: file %s not found in release", version, filename)
	}

	return "", fmt.Errorf("Go %s: version not found in release index", version)
}

// ResolveGoVersion extracts the full Go version from go.work or go.mod.
// Returns the complete version string (e.g. "1.26.1"), not stripped to major.minor.
// Falls back to "1.24" if no version directive found.
func ResolveGoVersion(dir, repoRoot string) string {
	// Prefer go.work at repo root (workspace mode)
	if ver := parseGoFullVersion(filepath.Join(repoRoot, "go.work")); ver != "" {
		return ver
	}
	// Try go.mod in module directory
	if ver := parseGoFullVersion(filepath.Join(dir, "go.mod")); ver != "" {
		return ver
	}
	// Try go.mod at repo root
	if dir != repoRoot {
		if ver := parseGoFullVersion(filepath.Join(repoRoot, "go.mod")); ver != "" {
			return ver
		}
	}
	return "1.24"
}

// parseGoFullVersion reads a go.mod or go.work file and returns the full go version.
// Prefers the toolchain directive over the go directive.
// Unlike the old parseGoVersion, this preserves the full version (e.g. "1.26.1").
func parseGoFullVersion(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var goVer string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// "toolchain go1.26.1" is a stronger signal than "go 1.26.1"
		if strings.HasPrefix(line, "toolchain ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return strings.TrimPrefix(fields[1], "go")
			}
		}
		if goVer == "" && strings.HasPrefix(line, "go ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				goVer = fields[1]
			}
		}
	}
	return goVer
}
