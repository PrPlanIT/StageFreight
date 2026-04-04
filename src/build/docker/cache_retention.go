package docker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// LocalRetentionResult records what the local cache retention executor did.
type LocalRetentionResult struct {
	Dir         string
	EntriesBefore int
	Pruned      int
	PrunedBytes int64
	Reason      string // "" if nothing to do
}

// localCacheRetentionPhase enforces local cache retention post-build.
// Runs only on success. Prunes by max_age first, then max_size oldest-first.
func localCacheRetentionPhase() pipeline.Phase {
	return pipeline.Phase{
		Name: "cache-retention-local",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			if !pc.Config.BuildCache.IsActive() {
				return nil, nil
			}
			localCfg := pc.Config.BuildCache.Local
			if localCfg.Retention.MaxAge == "" && localCfg.Retention.MaxSize == "" {
				return nil, nil
			}

			repoID := resolveRepoIDFromContext(pc)
			dir := LocalCacheDir(repoID, localCfg)

			result := enforceLocalRetention(dir, localCfg.Retention)
			renderLocalRetention(pc.Writer, pc.Color, result)

			summary := fmt.Sprintf("pruned %d entries", result.Pruned)
			if result.Pruned == 0 {
				summary = "within limits"
			}

			return &pipeline.PhaseResult{
				Name:    "cache-retention-local",
				Status:  "success",
				Summary: summary,
			}, nil
		},
	}
}

// enforceLocalRetention prunes local BuildKit cache by age then size.
// BuildKit's type=local cache stores blobs in the cache directory.
// We prune at the directory level — remove oldest entries first.
func enforceLocalRetention(dir string, retention config.LocalRetention) LocalRetentionResult {
	result := LocalRetentionResult{Dir: dir}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			result.Reason = "cache dir does not exist (no prior cache)"
			return result
		}
		result.Reason = fmt.Sprintf("cache dir unreadable: %v", err)
		return result
	}
	result.EntriesBefore = len(entries)

	// Phase 1: prune by max_age.
	if retention.MaxAge != "" {
		maxAge, err := parseDuration(retention.MaxAge)
		if err == nil && maxAge > 0 {
			cutoff := time.Now().Add(-maxAge)
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Before(cutoff) {
					path := filepath.Join(dir, e.Name())
					size := dirSize(path)
					if os.RemoveAll(path) == nil {
						result.Pruned++
						result.PrunedBytes += size
					}
				}
			}
		}
	}

	// Phase 2: enforce max_size by evicting oldest entries.
	if retention.MaxSize != "" {
		maxBytes, err := parseSize(retention.MaxSize)
		if err == nil && maxBytes > 0 {
			// Re-read after age pruning.
			entries, _ = os.ReadDir(dir)
			type entry struct {
				name    string
				size    int64
				modTime time.Time
			}
			var all []entry
			var totalSize int64
			for _, e := range entries {
				path := filepath.Join(dir, e.Name())
				size := dirSize(path)
				info, err := e.Info()
				if err != nil {
					continue
				}
				all = append(all, entry{name: e.Name(), size: size, modTime: info.ModTime()})
				totalSize += size
			}

			// Sort oldest first.
			sort.Slice(all, func(i, j int) bool {
				return all[i].modTime.Before(all[j].modTime)
			})

			for _, e := range all {
				if totalSize <= maxBytes {
					break
				}
				path := filepath.Join(dir, e.name)
				if os.RemoveAll(path) == nil {
					result.Pruned++
					result.PrunedBytes += e.size
					totalSize -= e.size
				}
			}
		}
	}

	return result
}

func renderLocalRetention(w interface{ Write([]byte) (int, error) }, color bool, result LocalRetentionResult) {
	sec := output.NewSection(w, "Cache Retention (local)", 0, color)
	sec.Row("%-14s%s", "path", result.Dir)
	sec.Row("%-14s%d", "entries", result.EntriesBefore)
	sec.Row("%-14s%d", "pruned", result.Pruned)
	if result.PrunedBytes > 0 {
		sec.Row("%-14s%s", "reclaimed", formatBytes(result.PrunedBytes))
	}
	if result.Reason != "" {
		sec.Row("%-14s%s", "note", result.Reason)
	}
	sec.Close()
}

// parseDuration parses a human duration like "7d", "72h", "30m".
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		s = strings.TrimSuffix(s, "d")
		var days int
		if _, err := fmt.Sscanf(s, "%d", &days); err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// parseSize parses a human size like "15GB", "500MB".
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	var multiplier int64 = 1
	switch {
	case strings.HasSuffix(s, "TB"):
		multiplier = 1024 * 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "TB")
	case strings.HasSuffix(s, "GB"):
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		multiplier = 1024
		s = strings.TrimSuffix(s, "KB")
	}
	var val int64
	if _, err := fmt.Sscanf(s, "%d", &val); err != nil {
		return 0, err
	}
	return val * multiplier, nil
}

// dirSize returns the total size of a directory tree.
func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

func formatBytes(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(b)/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// resolveRepoIDFromContext extracts a repo identifier from pipeline context.
func resolveRepoIDFromContext(pc *pipeline.PipelineContext) string {
	if pc.Config != nil && pc.Config.Sources.Primary.URL != "" {
		return pc.Config.Sources.Primary.URL
	}
	return os.Getenv("SF_CI_REPO_URL")
}
