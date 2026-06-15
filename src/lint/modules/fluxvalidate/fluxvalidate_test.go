package fluxvalidate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestContentGatingInert proves the adapter does nothing — and resolves no
// toolchain — in a repo with no Flux resources. The multi-modal guarantee: a
// pure build repo never pays for flux validation.
func TestContentGatingInert(t *testing.T) {
	dir := t.TempDir()
	// A plain YAML and a kustomize.config.k8s.io kustomization that is NOT a Flux
	// Kustomization — neither activates validation.
	mustWrite(t, filepath.Join(dir, "config.yaml"), "foo: bar\n")
	mustWrite(t, filepath.Join(dir, "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n")

	findings, err := (&module{}).CheckRepository(context.Background(), dir)
	if err != nil {
		t.Fatalf("CheckRepository error: %v", err)
	}
	if findings != nil {
		t.Fatalf("expected nil findings for non-Flux repo, got %v", findings)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
