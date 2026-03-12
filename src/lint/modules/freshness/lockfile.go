package freshness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

const lockFilePath = ".stagefreight/freshness.lock"

// DigestLock tracks non-versioned tag digests over time.
type DigestLock struct {
	Digests map[string]DigestEntry `yaml:"digests"`
}

// DigestEntry records a single image tag's digest.
type DigestEntry struct {
	Digest  string `yaml:"digest"`
	Checked string `yaml:"checked"`
}

// checkDigestLock handles non-versioned tags (e.g. "latest", "noble") by
// comparing manifest digests against a lock file.
func (m *freshnessModule) checkDigestLock(ctx context.Context, file lint.FileInfo, stages []stageInfo) []lint.Finding {
	var nonVersioned []stageInfo
	for _, s := range stages {
		_, tag := splitImageTag(s.Image)
		if tag == "" {
			tag = "latest"
		}
		dt := decomposeTag(tag)
		if dt.Version == nil {
			nonVersioned = append(nonVersioned, s)
		}
	}

	if len(nonVersioned) == 0 {
		return nil
	}

	rootDir := filepath.Dir(file.AbsPath)
	// Walk up to find repo root (where .stagefreight lives).
	// For simplicity, use the file's directory — the engine's RootDir
	// would be better but we don't have it here. The lock path is
	// relative to wherever .stagefreight/ exists.
	lockPath := filepath.Join(rootDir, lockFilePath)

	lock := loadLock(lockPath)
	var findings []lint.Finding
	changed := false

	for _, stage := range nonVersioned {
		image, tag := splitImageTag(stage.Image)
		if tag == "" {
			tag = "latest"
		}

		ref := normalizeImageRef(image) + ":" + tag

		// Resolve current manifest digest.
		digest, err := m.fetchManifestDigest(ctx, image, tag)
		if err != nil {
			continue
		}

		prev, exists := lock.Digests[ref]
		now := time.Now().UTC().Format(time.RFC3339)

		if !exists {
			// First run — record and move on.
			lock.Digests[ref] = DigestEntry{Digest: digest, Checked: now}
			changed = true
			continue
		}

		if prev.Digest != digest {
			findings = append(findings, lint.Finding{
				File:     file.Path,
				Line:     stage.Line,
				Module:   "freshness",
				Severity: lint.SeverityInfo,
				Message:  fmt.Sprintf("%s has a newer digest (last checked: %s)", ref, prev.Checked),
			})
			lock.Digests[ref] = DigestEntry{Digest: digest, Checked: now}
			changed = true
		}
	}

	if changed {
		_ = saveLock(lockPath, lock) // best-effort
	}

	return findings
}

// fetchManifestDigest queries the registry v2 manifest endpoint.
func (m *freshnessModule) fetchManifestDigest(ctx context.Context, image, tag string) (string, error) {
	namespace, repo := splitImageNamespace(image)
	ep := m.cfg.registryEndpoint(EcosystemDockerImage)
	defaultURL := fmt.Sprintf("https://registry.hub.docker.com/v2/repositories/%s/%s/tags/%s", namespace, repo, tag)
	url := m.cfg.registryURL(EcosystemDockerImage, defaultURL)
	if url != defaultURL {
		// Custom registry: use v2 manifests endpoint.
		url = strings.TrimRight(url, "/") + fmt.Sprintf("/%s/%s/manifests/%s", namespace, repo, tag)
	}

	var resp struct {
		Digest string `json:"digest"`
	}
	if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
		return "", err
	}
	if resp.Digest == "" {
		return "", fmt.Errorf("no digest for %s/%s:%s", namespace, repo, tag)
	}
	return resp.Digest, nil
}

// normalizeImageRef ensures a fully qualified image reference.
func normalizeImageRef(image string) string {
	if !containsDot(image) && !containsSlash(image) {
		return "docker.io/library/" + image
	}
	if !containsDot(image) {
		return "docker.io/" + image
	}
	return image
}

func containsDot(s string) bool {
	for _, c := range s {
		if c == '.' {
			return true
		}
	}
	return false
}

func containsSlash(s string) bool {
	for _, c := range s {
		if c == '/' {
			return true
		}
	}
	return false
}

func loadLock(path string) DigestLock {
	lock := DigestLock{Digests: make(map[string]DigestEntry)}
	data, err := os.ReadFile(path)
	if err != nil {
		return lock
	}
	_ = yaml.Unmarshal(data, &lock)
	if lock.Digests == nil {
		lock.Digests = make(map[string]DigestEntry)
	}
	return lock
}

func saveLock(path string, lock DigestLock) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
