package presetref

import (
	"os"
	"path/filepath"
	"time"
)

// FSCache is a filesystem-backed Cache rooted at a directory — typically a repo's
// .stagefreight/preset-cache. It stores each fetched preset under its (sanitized,
// slash-bearing but ..-free) CacheKey, so pinned refs and tracked-ref fallbacks persist
// across runs.
type FSCache struct {
	Root string
}

// NewFSCache returns an FSCache rooted at dir.
func NewFSCache(dir string) FSCache { return FSCache{Root: dir} }

func (c FSCache) Read(key string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(c.Root, filepath.FromSlash(key)))
	if err != nil {
		return nil, false
	}
	return data, true
}

func (c FSCache) Write(key string, content []byte) error {
	p := filepath.Join(c.Root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, content, 0o644)
}

// NoCache retains nothing. For a caller that fetches in order to write the content
// somewhere else — governance materializing a preset into a satellite's cache — where a
// second copy in the control repo would serve no one.
type NoCache struct{}

func (NoCache) Read(string) ([]byte, bool) { return nil, false }
func (NoCache) Write(string, []byte) error { return nil }

// Age reports how long ago the entry was retained, from its mtime.
func (c FSCache) Age(key string) (time.Duration, bool) {
	fi, err := os.Stat(filepath.Join(c.Root, filepath.FromSlash(key)))
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}
