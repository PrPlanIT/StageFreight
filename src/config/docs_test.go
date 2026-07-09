package config

import "testing"

// TestDefaultDocsConfig_ReferenceDocsOptIn locks the default: reference-docs generation
// is OFF by default. It produces StageFreight's own CLI/config reference and is only
// meaningful for StageFreight itself (which enables it explicitly); defaulting it on
// dumped those files into every downstream project. Badges/narrator/docker-readme stay
// on by default because they gracefully skip when the project configures nothing.
func TestDefaultDocsConfig_ReferenceDocsOptIn(t *testing.T) {
	g := DefaultDocsConfig().Generators
	if g.ReferenceDocs {
		t.Error("DefaultDocsConfig ReferenceDocs = true, want false (opt-in — it generates StageFreight's own docs)")
	}
	if !g.Badges || !g.Narrator || !g.DockerReadme {
		t.Errorf("badges/narrator/docker_readme should stay default-on (they skip when unconfigured): %+v", g)
	}
}
