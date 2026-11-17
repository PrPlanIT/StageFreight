package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMuslProvides: the names musl's loader answers itself need nothing installed.
func TestMuslProvides(t *testing.T) {
	for so, want := range map[string]bool{
		"libc.so": true, "libc.musl-x86_64.so.1": true, "ld-musl-aarch64.so.1": true,
		"libm.so.6": true, "libpthread.so.0": true, "libdl.so.2": true,
		"libgcc_s.so.1": false, "libstdc++.so.6": false, "libcrypt.so.1": false, "libcurl.so.4": false,
	} {
		if muslProvides(so) != want {
			t.Errorf("muslProvides(%q) = %v", so, !want)
		}
	}
	if m := missingLibraries([]string{"libc.so", "libgcc_s.so.1"}, t.TempDir()); len(m) != 1 || m[0] != "libgcc_s.so.1" {
		t.Errorf("missing %v", m)
	}
}

// TestUnlinkRustRuntime removes only lib/'s links into runtime-lib.
func TestUnlinkRustRuntime(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	os.MkdirAll(lib, 0o755)
	os.MkdirAll(filepath.Join(dir, runtimeLibDir), 0o755)
	os.WriteFile(filepath.Join(lib, "librustc_driver.so"), []byte("x"), 0o644)
	os.Symlink(filepath.Join("..", runtimeLibDir, "libgcc_s.so.1"), filepath.Join(lib, "libgcc_s.so.1"))
	os.Symlink("librustc_driver.so", filepath.Join(lib, "alias.so"))
	unlinkRustRuntime(dir)
	if _, err := os.Lstat(filepath.Join(lib, "libgcc_s.so.1")); !os.IsNotExist(err) {
		t.Error("the runtime link stays")
	}
	for _, keep := range []string{"librustc_driver.so", "alias.so"} {
		if _, err := os.Lstat(filepath.Join(lib, keep)); err != nil {
			t.Errorf("the toolchain's own %s was removed", keep)
		}
	}
}
