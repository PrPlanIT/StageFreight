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
	name, url, csum := pythonAsset("3.12.7", "x86_64")
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
}
