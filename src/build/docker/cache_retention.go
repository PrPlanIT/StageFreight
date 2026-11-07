package docker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// LocalRetentionResult records what promoting the local cache generation did.
type LocalRetentionResult struct {
	Dir      string
	Promoted bool  // this build's export is now the current generation
	Bytes    int64 // the current generation's size
	Reason   string
}

// promoteLocalCache makes this build's exported generation the repo's current one and
// removes the previous and any abandoned exports — only while no other run of the repo
// is using its cache, so a running build never loses the generation it imports. A
// generation larger than max_size is discarded.
func promoteLocalCache(dir string, retention config.LocalRetention) LocalRetentionResult {
	result := LocalRetentionResult{Dir: dir}
	next := localNext(dir)
	if _, err := os.Stat(next); err != nil {
		result.Reason = "no cache exported by this build"
		return result
	}
	if _, err := os.Stat(filepath.Join(next, "index.json")); err != nil {
		result.Reason = "cache export incomplete — discarded"
		if rmErr := paths.RemoveAllWritable(next); rmErr != nil {
			result.Reason = "cache export incomplete — not removed: " + rmErr.Error()
		}
		return result
	}
	ran, err := paths.WithUnitExclusive(dir, func() error {
		size := dirSize(next)
		if max, perr := config.ParseSize(retention.MaxSize); perr == nil && max > 0 && size > max {
			result.Reason = fmt.Sprintf("generation %s exceeds max_size %s — discarded", formatBytes(size), retention.MaxSize)
			return paths.RemoveAllWritable(next)
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range ents {
			p := filepath.Join(dir, e.Name())
			if e.IsDir() && p != next && (e.Name() == localCurrent || strings.HasPrefix(e.Name(), localNextPrefix)) {
				if err := paths.RemoveAllWritable(p); err != nil {
					return err
				}
			}
		}
		if err := os.Rename(next, filepath.Join(dir, localCurrent)); err != nil {
			return err
		}
		result.Promoted, result.Bytes = true, size
		return nil
	})
	switch {
	case err != nil:
		result.Reason = fmt.Sprintf("promote failed: %v", err)
		_ = paths.RemoveAllWritable(next)
	case !ran:
		result.Reason = "another run of this repo is using the cache — kept the current generation"
		_ = paths.RemoveAllWritable(next)
	}
	return result
}

func renderLocalRetention(w interface{ Write([]byte) (int, error) }, color bool, result LocalRetentionResult) {
	sec := output.NewSection(w, "Cache Retention (local)", 0, color)
	sec.Row("%-14s%s", "path", result.Dir)
	if result.Promoted {
		sec.Row("%-14s%s", "generation", formatBytes(result.Bytes))
	}
	if result.Reason != "" {
		sec.Row("%-14s%s", "note", result.Reason)
	}
	sec.Close()
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
	return repoIdentity(pc.Config)
}

// cacheScope is the cache identity of one build in a repo. Builds export under the same
// tag otherwise, each overwriting the last.
func cacheScope(repoID, buildName string) string {
	return repoID + "#" + buildName
}

// repoIdentity names the repo for cache scoping. Imports, exports, promotion and
// retention must all resolve the same identity or a build never finds its own cache.
func repoIdentity(cfg *config.Config) string {
	if cfg != nil {
		if url := config.PrimaryURL(cfg); url != "" {
			return url
		}
	}
	return os.Getenv("SF_CI_REPO_URL")
}
