package facts

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// TestMetadataDrivesProjectLicenseAndDescription verifies Phase C's consumer rewiring at
// the fact layer: when metadata: declares a license/description, {project.license} and
// {project.description} resolve from it (license is authoritative over the LICENSE scan;
// description falls back to metadata when none is injected explicitly, e.g. on scribe).
func TestMetadataDrivesProjectLicenseAndDescription(t *testing.T) {
	cfg := &config.Config{Metadata: config.MetadataConfig{
		License:     "Apache-2.0",
		Description: config.Scoped[config.StringOrList]{Default: config.StringOrList{"short desc", "long desc"}},
	}}
	dir := t.TempDir() // non-empty rootDir enables {project.*}; not a git repo, so no scanned license

	got := ScribeRegistry().ResolveOne("{project.license} | {project.description}", &Context{
		Config:  cfg,
		Version: &gitver.VersionInfo{Version: "1"},
		RootDir: dir,
	})
	if got != "Apache-2.0 | short desc" {
		t.Errorf("got %q, want %q", got, "Apache-2.0 | short desc")
	}
}
