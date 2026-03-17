package build

import (
	"os"
	"path/filepath"
	"strings"
)

// Detection holds everything discovered about a repo's build capabilities.
type Detection struct {
	RootDir      string // absolute path to repo root
	Dockerfiles  []DockerfileInfo
	Language     string   // "go", "rust", "node", "python", ""
	Lockfiles    []string // relative paths: go.mod, Cargo.toml, etc.
	MainPackages []string // detected main package paths (Go: cmd/foo, bar, .)
	GitInfo      *GitInfo
}

// GitInfo holds git repository metadata.
type GitInfo struct {
	Remote  string // origin URL
	Branch  string // current branch
	LatestTag string
	SHA     string
}

// DockerfileInfo describes a discovered Dockerfile.
type DockerfileInfo struct {
	Path        string  // relative path from repo root
	Stages      []Stage // parsed multistage stages
	Args        []string
	Expose      []string
	Healthcheck *string
}

// Stage describes a single FROM stage in a Dockerfile.
type Stage struct {
	Name      string // alias from "AS name", empty if unnamed
	BaseImage string // the FROM image reference
	Line      int    // line number of the FROM instruction
}

// languageIndicators maps lockfile names to detected language.
var languageIndicators = map[string]string{
	"go.mod":           "go",
	"go.sum":           "go",
	"Cargo.toml":       "rust",
	"Cargo.lock":       "rust",
	"package.json":     "node",
	"package-lock.json": "node",
	"yarn.lock":        "node",
	"pnpm-lock.yaml":   "node",
	"bun.lockb":        "node",
	"requirements.txt": "python",
	"Pipfile":          "python",
	"Pipfile.lock":     "python",
	"pyproject.toml":   "python",
	"poetry.lock":      "python",
	"Gemfile":          "ruby",
	"Gemfile.lock":     "ruby",
	"composer.json":    "php",
	"composer.lock":    "php",
}

// dockerfileNames are filenames recognized as Dockerfiles.
var dockerfileNames = []string{
	"Dockerfile",
	"Dockerfile.dev",
	"Dockerfile.production",
	"Dockerfile.build",
}

// dockerfileDirs are directories searched for Dockerfiles.
var dockerfileDirs = []string{
	".",
	"build",
	"docker",
}

// DetectRepo inspects a directory and returns build-relevant information.
func DetectRepo(rootDir string) (*Detection, error) {
	det := &Detection{RootDir: rootDir}

	// Find Dockerfiles
	for _, dir := range dockerfileDirs {
		for _, name := range dockerfileNames {
			path := filepath.Join(rootDir, dir, name)
			if _, err := os.Stat(path); err == nil {
				rel, _ := filepath.Rel(rootDir, path)
				info, parseErr := ParseDockerfile(path)
				if parseErr != nil {
					// Still record the Dockerfile even if parsing fails
					info = &DockerfileInfo{Path: rel}
				} else {
					info.Path = rel
				}
				det.Dockerfiles = append(det.Dockerfiles, *info)
			}
		}
		// Also check for *.dockerfile pattern
		pattern := filepath.Join(rootDir, dir, "*.dockerfile")
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			rel, _ := filepath.Rel(rootDir, match)
			info, parseErr := ParseDockerfile(match)
			if parseErr != nil {
				info = &DockerfileInfo{Path: rel}
			} else {
				info.Path = rel
			}
			det.Dockerfiles = append(det.Dockerfiles, *info)
		}
	}

	// Detect language from lockfiles
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return det, nil // non-fatal
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if lang, ok := languageIndicators[name]; ok {
			det.Lockfiles = append(det.Lockfiles, name)
			if det.Language == "" {
				det.Language = lang
			}
		}
	}

	// Detect Go main packages if language is Go
	if det.Language == "go" {
		gb := NewGoBuild(false)
		mains, _ := gb.DetectMainPackages(rootDir)
		det.MainPackages = mains
	}

	// Detect git info
	det.GitInfo = detectGitInfo(rootDir)

	return det, nil
}

// detectGitInfo reads basic git info from the repo.
func detectGitInfo(rootDir string) *GitInfo {
	info := &GitInfo{}

	// Read HEAD for current branch
	headPath := filepath.Join(rootDir, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return nil
	}
	head := strings.TrimSpace(string(data))
	if strings.HasPrefix(head, "ref: refs/heads/") {
		info.Branch = strings.TrimPrefix(head, "ref: refs/heads/")
	}

	// Read origin remote
	configPath := filepath.Join(rootDir, ".git", "config")
	configData, err := os.ReadFile(configPath)
	if err == nil {
		info.Remote = parseGitRemoteOrigin(string(configData))
	}

	return info
}

// parseGitRemoteOrigin extracts the origin remote URL from git config.
func parseGitRemoteOrigin(config string) string {
	inOrigin := false
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if line == `[remote "origin"]` {
			inOrigin = true
			continue
		}
		if inOrigin {
			if strings.HasPrefix(line, "[") {
				break
			}
			if strings.HasPrefix(line, "url = ") {
				return strings.TrimPrefix(line, "url = ")
			}
		}
	}
	return ""
}
