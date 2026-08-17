package promote

import (
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
)

// OCILabel must read the org.opencontainers.image.revision that publish's provenance
// gate compares against the source HEAD. A present label is returned verbatim; an absent
// one yields "" (the gate treats that as "can't verify", never a mismatch).
func TestOCILabel_ReadsRevisionFromLayout(t *testing.T) {
	base := empty.Image
	cf, err := base.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	cf = cf.DeepCopy()
	cf.Config.Labels = map[string]string{"org.opencontainers.image.revision": "35a2d0f"}
	img, err := mutate.ConfigFile(base, cf)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	idx := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img})
	if _, err := layout.Write(dir, idx); err != nil {
		t.Fatalf("layout.Write: %v", err)
	}
	dig, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}

	got, err := OCILabel(dir, dig.String(), "org.opencontainers.image.revision")
	if err != nil {
		t.Fatalf("OCILabel: %v", err)
	}
	if got != "35a2d0f" {
		t.Errorf("revision = %q, want 35a2d0f", got)
	}

	// A label the image does not carry resolves to "" (not an error) — the gate then
	// declines to assert rather than failing a legitimately-unlabeled artifact.
	absent, err := OCILabel(dir, dig.String(), "org.opencontainers.image.version")
	if err != nil {
		t.Fatalf("OCILabel(absent): %v", err)
	}
	if absent != "" {
		t.Errorf("absent label = %q, want empty", absent)
	}
}
