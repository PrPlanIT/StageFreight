package anchordoc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/sign/provision"
)

func TestRender_DisclosesTierHonestly(t *testing.T) {
	out := Render(&provision.Identity{Tier: provision.TierSoftware, Fingerprint: "sha256:abc"}, "PUBKEYBYTES\n")
	for _, want := range []string{
		"## Signing Trust Anchor",
		"Tier-0 (persistent software key)",
		"sha256:abc",
		"Transparency log: no",
		"Non-exportable key: no",
		"PUBKEYBYTES",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

func TestUpdate_CreatesFileWhenAbsent(t *testing.T) {
	f := filepath.Join(t.TempDir(), "SECURITY.md")
	if err := Update(f, "SECTION\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(f)
	if !strings.Contains(string(data), StartMarker) || !strings.Contains(string(data), "SECTION") {
		t.Errorf("file not created with managed section:\n%s", data)
	}
}

func TestUpdate_PreservesProseReplacesOnlySection(t *testing.T) {
	f := filepath.Join(t.TempDir(), "SECURITY.md")
	original := "# Security Policy\n\nContact: security@example.com\n\n" +
		StartMarker + "\nOLD ANCHOR\n" + EndMarker + "\n\n## Disclosure\n\nReport here.\n"
	if err := os.WriteFile(f, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Update(f, "NEW ANCHOR\n"); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, f)
	if !strings.Contains(got, "Contact: security@example.com") || !strings.Contains(got, "## Disclosure") {
		t.Errorf("operator prose was lost:\n%s", got)
	}
	if !strings.Contains(got, "NEW ANCHOR") || strings.Contains(got, "OLD ANCHOR") {
		t.Errorf("managed section not replaced:\n%s", got)
	}
}

func TestUpdate_Idempotent(t *testing.T) {
	f := filepath.Join(t.TempDir(), "SECURITY.md")
	if err := Update(f, "SECTION\n"); err != nil {
		t.Fatal(err)
	}
	first := mustRead(t, f)
	if err := Update(f, "SECTION\n"); err != nil {
		t.Fatal(err)
	}
	if second := mustRead(t, f); first != second {
		t.Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func mustRead(t *testing.T, f string) string {
	t.Helper()
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
