package fluxvalidate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyStatus(t *testing.T) {
	cases := map[string]string{
		"statusValid":    "valid",
		"statusInvalid":  "invalid", // must not match "valid"
		"statusError":    "error",
		"statusSkipped":  "skipped",
		"VALID":          "valid",
		"something-else": "other",
	}
	for in, want := range cases {
		if got := classifyStatus(in); got != want {
			t.Errorf("classifyStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortedKinds_ByCountThenName(t *testing.T) {
	got := sortedKinds(map[string]int{"B": 1, "A": 3, "C": 1})
	want := []string{"A", "B", "C"} // 3 first, then count-1 ties broken lexically
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("sortedKinds = %v, want %v", got, want)
	}
}

// TestContentGatingInert proves the module does nothing — and resolves no
// toolchain — in a repo with no Flux resources. This is the multi-modal
// guarantee: a pure build repo never pays for flux validation.
func TestContentGatingInert(t *testing.T) {
	dir := t.TempDir()
	// A plain, non-Flux YAML and a kustomize.config.k8s.io kustomization that
	// is NOT a Flux Kustomization — neither should activate the module.
	mustWrite(t, filepath.Join(dir, "config.yaml"), "foo: bar\n")
	mustWrite(t, filepath.Join(dir, "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n")

	m := &module{}
	findings, err := m.CheckRepository(context.Background(), dir)
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
