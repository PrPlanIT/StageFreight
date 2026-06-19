package toolchain

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// isGoMajorMinor reports whether v is a bare "major.minor" (e.g. "1.24") with no
// patch component — the normal form of a go.mod `go` directive, but NOT a
// downloadable release (go.dev/dl publishes only full patch versions).
func isGoMajorMinor(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// resolveLatestGoPatch resolves a bare "major.minor" (e.g. "1.24") to its latest
// stable patch ("1.24.7") via the Go release index. Required because a `go 1.24`
// directive — or the resolver fallback — is not itself a downloadable toolchain.
//
// It also returns the SHA256 for the goos/goarch archive of the resolved patch,
// extracted from the same index document, so the caller need not fetch the index
// a second time for the checksum. The checksum may be "" if that platform's file
// is absent — the caller then falls back to fetchGoChecksum.
func resolveLatestGoPatch(majorMinor, goos, goarch string) (version, checksum string, err error) {
	resp, err := httpGet("https://go.dev/dl/?mode=json&include=all")
	if err != nil {
		return "", "", fmt.Errorf("fetching Go release index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("Go release index: HTTP %d", resp.StatusCode)
	}
	var releases []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
		Files   []struct {
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", "", fmt.Errorf("parsing Go release index: %w", err)
	}
	prefix := "go" + majorMinor + "." // "go1.24."
	bestPatch := -1
	for _, rel := range releases {
		if !rel.Stable || !strings.HasPrefix(rel.Version, prefix) {
			continue
		}
		patch, perr := strconv.Atoi(strings.TrimPrefix(rel.Version, prefix))
		if perr != nil {
			continue // skip prerelease forms like go1.24.0rc1
		}
		if patch <= bestPatch {
			continue
		}
		bestPatch = patch
		version = strings.TrimPrefix(rel.Version, "go")
		checksum = ""
		want := fmt.Sprintf("go%s.%s-%s.tar.gz", version, goos, goarch)
		for _, f := range rel.Files {
			if f.Filename == want {
				checksum = f.SHA256
				break
			}
		}
	}
	if version == "" {
		return "", "", fmt.Errorf("Go %s: no stable patch release found in index", majorMinor)
	}
	return version, checksum, nil
}

// ResolveGoVersion extracts the Go version directive from go.work or go.mod: the
// `toolchain` directive if present (full, e.g. "1.26.1"), otherwise the `go`
// directive (normally a bare major.minor, e.g. "1.24"). It returns the directive
// verbatim — a semantic version, NOT necessarily a downloadable one. Canonicalizing
// a bare major.minor to its latest stable patch happens at distribution time in
// resolveGo, which is where a concrete downloadable toolchain is actually required.
// Falls back to "1.24" when no directive is found.
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
