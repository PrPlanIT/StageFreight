package toolchain

import (
	"os"
	"path/filepath"
)

const cacheSubdir = ".stagefreight/toolchains"

// CacheRoot returns the toolchain cache root directory.
// Primary: .stagefreight/toolchains/ relative to workspace.
func CacheRoot(rootDir string) string {
	return filepath.Join(rootDir, cacheSubdir)
}

// CacheDir returns the versioned install directory for a tool.
// e.g. .stagefreight/toolchains/go/1.26.1/
func CacheDir(rootDir, tool, version string) string {
	return filepath.Join(CacheRoot(rootDir), tool, version)
}

// CacheBinPath returns the expected binary path within the cache.
// e.g. .stagefreight/toolchains/go/1.26.1/bin/go
func CacheBinPath(rootDir, tool, version, binary string) string {
	return filepath.Join(CacheDir(rootDir, tool, version), "bin", binary)
}

// EnsureCacheDir creates the cache directory structure if needed.
func EnsureCacheDir(rootDir, tool, version string) error {
	return os.MkdirAll(filepath.Join(CacheDir(rootDir, tool, version), "bin"), 0755)
}
