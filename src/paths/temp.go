package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var tempRoot = sync.OnceValue(func() string {
	name := fmt.Sprintf("stagefreight-%d", os.Getuid())
	if dir := os.TempDir(); filepath.Base(dir) == name {
		return dir // a StageFreight run started by another
	}
	return filepath.Join(os.TempDir(), name)
})

// TempRoot is this user's StageFreight directory in the system temp dir; disk GC expires
// its entries.
func TempRoot() string { return tempRoot() }

// UseTempRoot points TMPDIR at TempRoot for the run and every process it starts. TMPDIR is
// left as it is when the directory cannot be made, or exists but is not a 0700 directory
// owned by this user.
func UseTempRoot() {
	root := TempRoot()
	if err := os.Mkdir(root, 0o700); err != nil && !os.IsExist(err) {
		return
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return
	}
	_ = os.Setenv("TMPDIR", root)
}
