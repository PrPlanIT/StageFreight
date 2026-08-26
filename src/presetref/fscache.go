package presetref

import (
	"os"
	"path/filepath"
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
