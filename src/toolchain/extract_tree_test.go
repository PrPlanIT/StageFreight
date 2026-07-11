package toolchain

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTarGzTree(t *testing.T) {
	// Build a node-like tarball: top-level node-v1/ prefix, a file, and a symlink.
	arch := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(arch)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	write := func(name string, mode int64, body string, link string, typ byte) {
		h := &tar.Header{Name: name, Mode: mode, Typeflag: typ, Size: int64(len(body)), Linkname: link}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			tw.Write([]byte(body))
		}
	}
	write("node-v1/", 0755, "", "", tar.TypeDir)
	write("node-v1/bin/node", 0755, "NODEBIN", "", tar.TypeReg)
	write("node-v1/bin/npm", 0755, "", "../lib/node_modules/npm/bin/npm-cli.js", tar.TypeSymlink)
	write("node-v1/lib/node_modules/npm/bin/npm-cli.js", 0644, "NPMCLI", "", tar.TypeReg)
	tw.Close()
	gz.Close()
	f.Close()

	dest := t.TempDir()
	if err := extractTarGzTree(arch, dest, 1); err != nil {
		t.Fatalf("extract: %v", err)
	}
	// strip 1 → bin/node directly under dest, content preserved
	if b, err := os.ReadFile(filepath.Join(dest, "bin", "node")); err != nil || string(b) != "NODEBIN" {
		t.Errorf("bin/node = %q, %v", b, err)
	}
	// the npm symlink survives (not ignored) and resolves in-tree
	if fi, err := os.Lstat(filepath.Join(dest, "bin", "npm")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("bin/npm should be a symlink, got %v (err %v)", fi, err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "lib", "node_modules", "npm", "bin", "npm-cli.js")); err != nil || string(b) != "NPMCLI" {
		t.Errorf("npm-cli.js = %q, %v", b, err)
	}
}
