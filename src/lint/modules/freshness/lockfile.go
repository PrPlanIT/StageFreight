package freshness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/discovery"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

const lockFilePath = ".stagefreight/freshness.lock"

// checkDigestLock handles non-versioned tags (e.g. "latest", "noble") by
// comparing manifest digests against a lock file.
func (m *freshnessModule) checkDigestLock(ctx context.Context, file lint.FileInfo, stages []supplychain.StageInfo) []lint.Finding {
	var nonVersioned []supplychain.StageInfo
	for _, s := range stages {
		_, tag := discovery.SplitImageTag(s.Image)
		if tag == "" {
			tag = "latest"
		}
		dt := version.DecomposeTag(tag)
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
		image, tag := discovery.SplitImageTag(stage.Image)
		if tag == "" {
			tag = "latest"
		}

		ref := normalizeImageRef(image) + ":" + tag

		// Resolve current manifest digest.
		digest, err := m.resolver.FetchManifestDigest(ctx, image, tag)
		if err != nil {
			continue
		}

		prev, exists := lock.Digests[ref]
		now := time.Now().UTC().Format(time.RFC3339)

		if !exists {
			// First run — record and move on.
			lock.Digests[ref] = supplychain.DigestEntry{Digest: digest, Checked: now}
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
			lock.Digests[ref] = supplychain.DigestEntry{Digest: digest, Checked: now}
			changed = true
		}
	}

	if changed {
		_ = saveLock(lockPath, lock) // best-effort
	}

	return findings
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

func loadLock(path string) supplychain.DigestLock {
	lock := supplychain.DigestLock{Digests: make(map[string]supplychain.DigestEntry)}
	data, err := os.ReadFile(path)
	if err != nil {
		return lock
	}
	_ = yaml.Unmarshal(data, &lock)
	if lock.Digests == nil {
		lock.Digests = make(map[string]supplychain.DigestEntry)
	}
	return lock
}

func saveLock(path string, lock supplychain.DigestLock) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
