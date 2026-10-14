package scribe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/manifest"
)

// The SBOM describes one scanned image. Its packages fill the detected inventories of
// that build's manifest only - never another build's.
func TestImportDetectedOnlyForTheScannedBuild(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".stagefreight", "security")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sbom := `{"packages":[{"name":"frappe","versionInfo":"16.51.0","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:pypi/frappe@16.51.0"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "sbom.spdx.json"), []byte(sbom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sbom.subject.json"), []byte(`{"build":"image","ref":"example/app@sha256:abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Security: config.SecurityConfig{OutputDir: ".stagefreight/security"}}

	same := &manifest.Manifest{}
	importDetected(same, cfg, root, "image")
	if len(same.Detected.Pip) != 1 || same.Detected.Pip[0].Name != "frappe" {
		t.Errorf("same build: got %+v", same.Detected.Pip)
	}

	other := &manifest.Manifest{}
	importDetected(other, cfg, root, "sidecar")
	if len(other.Detected.Pip) != 0 || other.Completeness.SBOMImported {
		t.Errorf("another build's manifest must not take this SBOM: got %+v", other.Detected.Pip)
	}
}
