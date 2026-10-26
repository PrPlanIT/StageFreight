package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Use markers live beside .metadata.json in each install directory. Their modification
// time is the record: lastUsedFile is when any run last used the install, pinnedUsedFile
// when a run that declares that exact version last used it. The cache is shared by every
// repo on a runner, so retention ranks by these rather than by install time, and keeps
// what any repo still uses whichever repo's run prunes.
const (
	lastUsedFile   = ".last_used"
	pinnedUsedFile = ".pinned_used"
)

// markUsed records that r's install was just used, and pinned when its version was
// declared exactly. Best effort: an install in a read-only root records nothing.
func markUsed(rootDir string, r Result, pinned bool) {
	dir := installDirOf(rootDir, r)
	if dir == "" {
		return
	}
	touch(filepath.Join(dir, lastUsedFile))
	if pinned {
		touch(filepath.Join(dir, pinnedUsedFile))
	}
}

// installDirOf is the <root>/<tool>/<version> directory holding r's binary.
func installDirOf(rootDir string, r Result) string {
	for _, root := range append(ReadRoots(rootDir), InstallRoot(rootDir)) {
		prefix := filepath.Join(root, r.Tool) + string(filepath.Separator)
		if rest, ok := strings.CutPrefix(r.Path, prefix); ok {
			if ver, _, ok := strings.Cut(rest, string(filepath.Separator)); ok {
				return filepath.Join(root, r.Tool, ver)
			}
		}
	}
	return ""
}

func touch(path string) {
	now := time.Now()
	if os.Chtimes(path, now, now) == nil {
		return
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_ = f.Close()
	}
}

// usedAt is the modification time of an install's marker, or zero when absent.
func usedAt(dir, marker string) time.Time {
	if fi, err := os.Stat(filepath.Join(dir, marker)); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}
