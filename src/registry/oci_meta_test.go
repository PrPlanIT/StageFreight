package registry

import (
	"encoding/json"
	"testing"
)

// A concrete image manifest's size is config + all layers; a multi-arch index carries no
// sizes itself and must be resolved to a platform sub-manifest (linux/amd64 preferred).
func TestOCIManifestSizeAndPlatformPick(t *testing.T) {
	const concrete = `{
		"config": {"size": 1000, "digest": "sha256:cfg"},
		"layers": [{"size": 5000}, {"size": 250}]
	}`
	var m ociManifest
	if err := json.Unmarshal([]byte(concrete), &m); err != nil {
		t.Fatal(err)
	}
	if m.isIndex() {
		t.Fatal("concrete manifest misread as an index")
	}
	if got := m.totalSize(); got != 6250 {
		t.Errorf("totalSize = %d, want 6250 (1000 config + 5250 layers)", got)
	}

	const index = `{
		"manifests": [
			{"digest": "sha256:arm", "platform": {"os": "linux", "architecture": "arm64"}},
			{"digest": "sha256:amd", "platform": {"os": "linux", "architecture": "amd64"}}
		]
	}`
	var idx ociManifest
	if err := json.Unmarshal([]byte(index), &idx); err != nil {
		t.Fatal(err)
	}
	if !idx.isIndex() {
		t.Fatal("index misread as a concrete manifest")
	}
	if got := idx.pickPlatformDigest(); got != "sha256:amd" {
		t.Errorf("pickPlatformDigest = %q, want sha256:amd (linux/amd64 preferred)", got)
	}
}

// ociHost strips scheme and trailing slash so config URLs of either shape yield a bare host.
func TestOCIHost(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io":               "ghcr.io",
		"https://cr.pcfae.com/": "cr.pcfae.com",
		"http://localhost:5000": "localhost:5000",
		"  docker.io  ":         "docker.io",
	} {
		if got := ociHost(in); got != want {
			t.Errorf("ociHost(%q) = %q, want %q", in, got, want)
		}
	}
}
