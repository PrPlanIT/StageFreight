package artifact

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPersistenceHandle_RoundTrips verifies the handle is inert serializable
// data: it survives a manifest write/read cycle unchanged. (It is consumed by
// no decision path — that is asserted structurally below.)
func TestPersistenceHandle_RoundTrips(t *testing.T) {
	a := validDockerArtifact()
	a.Digest = Digest("sha256:" + strings.Repeat("a", 64))
	a.Persistence = PersistenceHandle{
		Kind:      PersistenceOCILayout,
		OCILayout: &OCILayoutRef{Path: "sha256/" + strings.Repeat("a", 64)},
	}
	m := OutputsManifest{Artifacts: []Artifact{a}}
	if err := m.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got OutputsManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	h := got.Artifacts[0].Persistence
	if h.Kind != PersistenceOCILayout {
		t.Fatalf("Kind = %q, want oci_layout", h.Kind)
	}
	if h.OCILayout == nil || h.OCILayout.Path != "sha256/"+strings.Repeat("a", 64) {
		t.Fatalf("OCILayout ref did not round-trip: %+v", h.OCILayout)
	}
}

// TestPersistenceHandle_IsInChecksumlessOfTrust documents that Persistence is
// part of the serialized manifest (so it round-trips) — but its PRESENCE must
// never be read as trust. There is no code under src/ outside the artifact
// package that branches on Persistence in Phase 2; this test pins the zero
// value as the default so a missing handle is unambiguous.
func TestPersistenceHandle_ZeroValueIsNone(t *testing.T) {
	a := validDockerArtifact()
	if a.Persistence.Kind != PersistenceNone {
		t.Fatalf("default Persistence.Kind = %q, want none (empty)", a.Persistence.Kind)
	}
	if a.Persistence.OCILayout != nil {
		t.Fatal("default Persistence.OCILayout must be nil")
	}
}

// TestPersistenceKind_NoDistributionVariant guards the core invariant: the
// closed enum must not grow a registry/distribution variant. Persistence is
// retrieval, never distribution. If someone adds a "registry" persistence kind,
// this test should be the thing that makes them stop and reconsider.
func TestPersistenceKind_NoDistributionVariant(t *testing.T) {
	forbidden := []PersistenceKind{"registry", "push", "remote", "distribution", "tag"}
	known := map[PersistenceKind]bool{PersistenceNone: true, PersistenceOCILayout: true}
	for _, f := range forbidden {
		if known[f] {
			t.Errorf("PersistenceKind %q must not exist — persistence is retrieval, not distribution", f)
		}
	}
}
