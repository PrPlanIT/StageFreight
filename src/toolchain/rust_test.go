package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

// rustHostTriple is the FIRST place the host GOOS/GOARCH projects to a Rust triple —
// the projection must be exact (a wrong triple = a 404 on the dist server).
func TestRustHostTriple(t *testing.T) {
	for _, c := range []struct{ os, arch, want string }{
		{"linux", "amd64", "x86_64-unknown-linux-gnu"},
		{"linux", "arm64", "aarch64-unknown-linux-gnu"},
		{"darwin", "amd64", "x86_64-apple-darwin"},
		{"darwin", "arm64", "aarch64-apple-darwin"},
		{"windows", "amd64", "x86_64-pc-windows-msvc"},
	} {
		if got := rustHostTriple(c.os, c.arch); got != c.want {
			t.Errorf("rustHostTriple(%s,%s) = %q, want %q", c.os, c.arch, got, c.want)
		}
	}
}

func TestRustDownloadURL(t *testing.T) {
	got := rustDownloadURL("1.83.0", "x86_64-unknown-linux-gnu")
	want := "https://static.rust-lang.org/dist/rust-1.83.0-x86_64-unknown-linux-gnu.tar.gz"
	if got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
}

// A build must be reproducible, so only an explicit NUMERIC pin is honored; the
// "stable"/"nightly" channels are moving targets and fall back to the default.
func TestResolveRustVersion(t *testing.T) {
	dir := t.TempDir()
	if got := ResolveRustVersion(dir, dir); got != defaultRustVersion {
		t.Errorf("no toolchain file → default, got %q", got)
	}

	write := func(content string) {
		if err := os.WriteFile(filepath.Join(dir, "rust-toolchain.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[toolchain]\nchannel = \"1.81.0\"\n")
	if got := ResolveRustVersion(dir, dir); got != "1.81.0" {
		t.Errorf("pinned channel, got %q", got)
	}
	write("[toolchain]\nchannel = \"stable\"\n")
	if got := ResolveRustVersion(dir, dir); got != defaultRustVersion {
		t.Errorf("non-numeric channel must fall back to default, got %q", got)
	}

	// Legacy bare rust-toolchain file.
	os.Remove(filepath.Join(dir, "rust-toolchain.toml"))
	if err := os.WriteFile(filepath.Join(dir, "rust-toolchain"), []byte("1.79.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveRustVersion(dir, dir); got != "1.79.0" {
		t.Errorf("legacy bare file, got %q", got)
	}
}
