package toolchain

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// TestInstallFromZip installs the one named binary from a zip archive (terraform's format).
func TestInstallFromZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "tool.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{"terraform": "#!/bin/sh\n", "LICENSE.txt": "license"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()

	dst := filepath.Join(dir, "bin", "terraform")
	if err := installFromZip(archive, dst, "terraform"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "#!/bin/sh\n" {
		t.Fatalf("installed %q, %v", got, err)
	}
	if err := installFromZip(archive, filepath.Join(dir, "bin", "x"), "absent"); err == nil {
		t.Error("a binary missing from the archive must error")
	}
}
