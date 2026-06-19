package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

// rustHostTriple is the FIRST place the host GOOS/GOARCH projects to a Rust triple —
// the projection must be exact (a wrong triple = a 404 on the dist server).
func TestRustHostTriple(t *testing.T) {
	for _, c := range []struct{ os, arch, libc, want string }{
		{"linux", "amd64", "gnu", "x86_64-unknown-linux-gnu"},
		{"linux", "amd64", "musl", "x86_64-unknown-linux-musl"}, // Alpine host
		{"linux", "arm64", "musl", "aarch64-unknown-linux-musl"},
		{"linux", "amd64", "", "x86_64-unknown-linux-gnu"}, // empty libc → gnu default
		{"darwin", "amd64", "", "x86_64-apple-darwin"},
		{"darwin", "arm64", "", "aarch64-apple-darwin"},
		{"windows", "amd64", "", "x86_64-pc-windows-msvc"},
	} {
		if got := rustHostTriple(c.os, c.arch, c.libc); got != c.want {
			t.Errorf("rustHostTriple(%s,%s,%s) = %q, want %q", c.os, c.arch, c.libc, got, c.want)
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

// ResolveRustVersion honors a numeric pin AND a named channel; default is "stable"
// (resolved to a concrete version at download time), NOT a stale numeric default that
// would fail to compile a newer edition (the jetpack edition-2024 case).
func TestResolveRustVersion(t *testing.T) {
	dir := t.TempDir()
	if got := ResolveRustVersion(dir, dir); got != defaultRustChannel {
		t.Errorf("no toolchain file → default channel %q, got %q", defaultRustChannel, got)
	}

	write := func(content string) {
		if err := os.WriteFile(filepath.Join(dir, "rust-toolchain.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[toolchain]\nchannel = \"1.81.0\"\n")
	if got := ResolveRustVersion(dir, dir); got != "1.81.0" {
		t.Errorf("numeric pin, got %q", got)
	}
	// A named channel is honored (resolved to a concrete version later) — this is the
	// common real-world case (jetpack pins `stable` for edition 2024).
	write("[toolchain]\nchannel = \"stable\"\n")
	if got := ResolveRustVersion(dir, dir); got != "stable" {
		t.Errorf("named channel must be honored, got %q", got)
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
