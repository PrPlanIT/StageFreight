package toolchain

import (
	"strings"
	"testing"
)

func TestPythonArch(t *testing.T) {
	if got := pythonArch("amd64"); got != "x86_64" {
		t.Errorf("amd64 → %q, want x86_64", got)
	}
	if got := pythonArch("arm64"); got != "aarch64" {
		t.Errorf("arm64 → %q, want aarch64", got)
	}
}

func TestPythonAsset(t *testing.T) {
	// glibc (default)
	name, url, csum, cacheVer, platform := pythonAsset("3.12.7", "x86_64", false)
	wantName := "cpython-3.12.7+" + pbsReleaseTag + "-x86_64-unknown-linux-gnu-install_only.tar.gz"
	if name != wantName {
		t.Errorf("name = %q, want %q", name, wantName)
	}
	if !strings.Contains(url, "astral-sh/python-build-standalone/releases/download/"+pbsReleaseTag+"/"+name) {
		t.Errorf("url = %q", url)
	}
	if csum != url+".sha256" {
		t.Errorf("checksumURL = %q, want %q", csum, url+".sha256")
	}
	if cacheVer != "3.12.7" || platform != "linux/x86_64" {
		t.Errorf("glibc: cacheVer=%q platform=%q, want 3.12.7 / linux/x86_64", cacheVer, platform)
	}

	// musl (Alpine): unofficial-builds has no bearing here — pbs publishes the musl
	// install_only build itself; only the libc token + cache key change.
	mName, _, _, mCacheVer, mPlatform := pythonAsset("3.12.7", "x86_64", true)
	wantMuslName := "cpython-3.12.7+" + pbsReleaseTag + "-x86_64-unknown-linux-musl-install_only.tar.gz"
	if mName != wantMuslName {
		t.Errorf("musl name = %q, want %q", mName, wantMuslName)
	}
	if mCacheVer != "3.12.7-musl" || mPlatform != "linux/x86_64-musl" {
		t.Errorf("musl: cacheVer=%q platform=%q, want 3.12.7-musl / linux/x86_64-musl", mCacheVer, mPlatform)
	}
	// The libc-variant cache keys MUST differ, or the trees collide and the wrong binary
	// gets served on a mismatched host.
	if cacheVer == mCacheVer {
		t.Fatalf("glibc and musl cache keys must differ, both were %q", cacheVer)
	}
}
