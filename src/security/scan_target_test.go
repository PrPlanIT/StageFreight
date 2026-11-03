package security

import (
	"strings"
	"testing"
)

// TestAppendTrivyTarget_OCILayout proves an OCI layout target becomes trivy's
// --input <dir> (layout read, no daemon/registry), while a plain image ref is
// passed through unchanged.
func TestAppendTrivyTarget_OCILayout(t *testing.T) {
	got := appendTrivyTarget([]string{"image", "--format", "json"}, ociLayoutPrefix+"/store/sha256/abc")
	// must contain --input /store/sha256/abc and NOT the bare prefixed string
	foundInput := false
	for i, a := range got {
		if a == "--input" {
			if i+1 < len(got) && got[i+1] == "/store/sha256/abc" {
				foundInput = true
			}
		}
		if a == ociLayoutPrefix+"/store/sha256/abc" {
			t.Fatalf("layout prefix leaked into trivy args: %v", got)
		}
	}
	if !foundInput {
		t.Fatalf("expected --input <dir> for OCI layout target, got %v", got)
	}
}

// A published reference is scanned from its registry; anything else (a tarball, a local
// tag) is passed as given, for the scanner to resolve.
func TestScanTargets_PublishedVsGiven(t *testing.T) {
	trivy := func(target string) []string { return appendTrivyTarget([]string{"image"}, target) }
	cases := []struct {
		target, wantGrype string
		wantTrivy         []string
	}{
		{registryRefPrefix + "docker.io/org/app@sha256:abc", "registry:docker.io/org/app@sha256:abc",
			[]string{"image", "--image-src", "remote", "docker.io/org/app@sha256:abc"}},
		{"./image.tar", "./image.tar", []string{"image", "./image.tar"}},
		{"myorg/app:latest", "myorg/app:latest", []string{"image", "myorg/app:latest"}},
		{ociLayoutPrefix + "/store/x", "oci-dir:/store/x", []string{"image", "--input", "/store/x"}},
	}
	for _, c := range cases {
		if got := grypeTarget(c.target); got != c.wantGrype {
			t.Errorf("grypeTarget(%q) = %q, want %q", c.target, got, c.wantGrype)
		}
		if got := trivy(c.target); strings.Join(got, " ") != strings.Join(c.wantTrivy, " ") {
			t.Errorf("trivy(%q) = %v, want %v", c.target, got, c.wantTrivy)
		}
	}
}

// Registry credentials reach the scanners only for an image ref; a layout scan
// reads from disk and gets the plain CleanEnv.
func TestScannerEnv_RegistryAuthOnlyForImageRefs(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "/creds")
	hasAuth := func(env []string) bool {
		for _, kv := range env {
			if kv == "DOCKER_CONFIG=/creds" {
				return true
			}
		}
		return false
	}
	if !hasAuth(scannerEnv("docker.io/org/app@sha256:abc")) {
		t.Fatal("image-ref scan must carry DOCKER_CONFIG")
	}
	if hasAuth(scannerEnv(ociLayoutPrefix + "/store/x")) {
		t.Fatal("layout scan must not carry registry credentials")
	}
}
