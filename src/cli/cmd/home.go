package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ensureWritableHome gives a run that can't write its HOME a private directory for its
// state — an image run as an arbitrary uid has a HOME it doesn't own. An unset or
// missing HOME is replaced; an existing read-only one is kept, so the credentials in it
// stay readable, and only the write targets that are unset move to the private one.
func ensureWritableHome() {
	home := os.Getenv("HOME")
	if home != "" && dirWritable(home) {
		return
	}
	dir := privateHome()
	if dir == "" {
		return
	}
	if fi, err := os.Stat(home); home == "" || err != nil || !fi.IsDir() {
		_ = os.Setenv("HOME", dir)
		return
	}
	setIfUnset("XDG_CACHE_HOME", filepath.Join(dir, ".cache"))
	setIfUnset("BUILDX_CONFIG", filepath.Join(dir, ".docker", "buildx"))
}

// privateHome is <tmp>/stagefreight-home-<uid>, kept only while it is a real directory
// this uid owns and nobody else can enter; otherwise a fresh temporary directory.
// "" when neither can be made.
func privateHome() string {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("stagefreight-home-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err == nil || os.IsExist(err) {
		if ownedPrivateDir(dir) {
			return dir
		}
	}
	tmp, err := os.MkdirTemp("", "stagefreight-home-")
	if err != nil {
		return ""
	}
	return tmp
}

// ownedPrivateDir reports whether dir is a directory (not a symlink) owned by this uid
// with no group or other access.
func ownedPrivateDir(dir string) bool {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func setIfUnset(key, val string) {
	if _, ok := os.LookupEnv(key); !ok {
		_ = os.Setenv(key, val)
	}
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".sf-home-probe-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}
