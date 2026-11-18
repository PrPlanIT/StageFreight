package gitver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/license"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

// ProjectMeta holds project-level metadata resolved from git and filesystem.
type ProjectMeta struct {
	Name     string // repo name (last path component of git remote)
	URL      string // repo URL (git remote origin)
	License  string // SPDX identifier from LICENSE file
	Language string // auto-detected from lockfiles
	Module   string // canonical module/package name, per the build manifest
}

// DetectProject resolves project metadata from git remote, LICENSE file, and lockfiles.
func DetectProject(rootDir string) *ProjectMeta {
	pm := &ProjectMeta{}

	// Name and URL from git remote origin
	if repo, err := gitstate.OpenRepo(rootDir); err == nil {
		if remote, err := gitstate.RemoteURL(repo, "origin"); err == nil && remote != "" {
			pm.URL = remoteToHTTPS(remote)
			pm.Name = repoNameFromRemote(remote)
		}
	}

	// License from LICENSE file
	pm.License = detectLicense(rootDir)

	// Language from lockfiles
	pm.Language = detectLanguage(rootDir)

	// Module/package name from the build manifest
	pm.Module = detectModule(rootDir)

	return pm
}

// repoNameFromRemote extracts the repository name from a git remote URL.
// Handles SSH (git@host:org/repo.git) and HTTPS (https://host/org/repo.git).
func repoNameFromRemote(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")

	// SSH: git@host:org/repo
	if idx := strings.LastIndex(remote, ":"); idx != -1 && !strings.Contains(remote, "://") {
		remote = remote[idx+1:]
	}

	// Last path component
	if idx := strings.LastIndex(remote, "/"); idx != -1 {
		return remote[idx+1:]
	}
	return remote
}

// remoteToHTTPS converts a git remote URL to HTTPS format for display.
// SSH remotes (git@host:org/repo.git) become https://host/org/repo.
// HTTPS remotes pass through with .git stripped.
func remoteToHTTPS(remote string) string {
	remote = strings.TrimSuffix(remote, ".git")

	// Already HTTPS
	if strings.HasPrefix(remote, "https://") || strings.HasPrefix(remote, "http://") {
		return remote
	}

	// SSH: git@host:org/repo → https://host/org/repo
	if idx := strings.Index(remote, "@"); idx != -1 {
		rest := remote[idx+1:]
		rest = strings.Replace(rest, ":", "/", 1)
		return "https://" + rest
	}

	return remote
}

// detectLicense reads the LICENSE file and returns an SPDX identifier.
func detectLicense(rootDir string) string {
	return license.Detect(func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(rootDir, name)) })
}

// detectLanguage identifies the primary programming language from lockfiles/manifests.
// ProjectModule returns the project's canonical module/package name (the {project.module}
// fact) from the build manifest in rootDir. Exported for callers that need only the module
// without the full DetectProject (git remote / license / language) pass — e.g. resolving
// {project.module} in build-arg templates.
func ProjectModule(rootDir string) string {
	return detectModule(rootDir)
}

// detectModule returns the project's canonical module/package name, dispatched by the
// build manifest present in rootDir: go.mod's module path, Cargo.toml's [package] name,
// package.json's name, or pyproject.toml's [project] name. This is the {project.module}
// fact — the value a shared build preset embeds (e.g. a Go binary's ldflags version
// path) so ONE preset serves a whole bucket of repos. Returns "" when no recognized
// manifest is present. Dispatch order matches detectLanguage's indicator precedence.
func detectModule(rootDir string) string {
	if m := goModulePath(filepath.Join(rootDir, "go.mod")); m != "" {
		return m
	}
	if m := tomlSectionName(filepath.Join(rootDir, "Cargo.toml"), "package"); m != "" {
		return m
	}
	if m := jsonName(filepath.Join(rootDir, "package.json")); m != "" {
		return m
	}
	if m := tomlSectionName(filepath.Join(rootDir, "pyproject.toml"), "project"); m != "" {
		return m
	}
	return ""
}

// goModulePath reads the module path from a go.mod ("module <path>").
func goModulePath(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// tomlSectionName reads `name = "..."` from the given [section] of a minimal TOML file
// (Cargo.toml [package], pyproject.toml [project]). A hand parse — no TOML dependency —
// sufficient for the single key we need; returns "" if the section or key is absent.
func tomlSectionName(path, section string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inSection = t == "["+section+"]"
			continue
		}
		if !inSection {
			continue
		}
		if rest, ok := strings.CutPrefix(t, "name"); ok {
			if rest = strings.TrimSpace(rest); strings.HasPrefix(rest, "=") {
				return strings.Trim(strings.TrimSpace(strings.TrimPrefix(rest, "=")), `"'`)
			}
		}
	}
	return ""
}

// jsonName reads the top-level "name" from a package.json.
func jsonName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Name
}

func detectLanguage(rootDir string) string {
	indicators := map[string]string{
		"go.mod":            "go",
		"Cargo.toml":        "rust",
		"package.json":      "node",
		"package-lock.json": "node",
		"yarn.lock":         "node",
		"pnpm-lock.yaml":    "node",
		"bun.lockb":         "node",
		"requirements.txt":  "python",
		"Pipfile":           "python",
		"pyproject.toml":    "python",
		"Gemfile":           "ruby",
		"composer.json":     "php",
	}

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if lang, ok := indicators[entry.Name()]; ok {
			return lang
		}
	}
	return ""
}
