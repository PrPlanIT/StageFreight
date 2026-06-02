package security

import "testing"

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

func TestAppendTrivyTarget_ImageRef(t *testing.T) {
	got := appendTrivyTarget([]string{"image"}, "docker.io/org/app@sha256:abc")
	last := got[len(got)-1]
	if last != "docker.io/org/app@sha256:abc" {
		t.Fatalf("image ref should pass through unchanged, got %q", last)
	}
}

// TestGrypeTarget proves the grype/syft source syntax: oci-dir:<dir> for a
// layout, bare ref otherwise.
func TestGrypeTarget(t *testing.T) {
	if got := grypeTarget(ociLayoutPrefix + "/store/x"); got != "oci-dir:/store/x" {
		t.Fatalf("layout → %q, want oci-dir:/store/x", got)
	}
	if got := grypeTarget("docker.io/org/app:tag"); got != "docker.io/org/app:tag" {
		t.Fatalf("image ref → %q, want unchanged", got)
	}
}
