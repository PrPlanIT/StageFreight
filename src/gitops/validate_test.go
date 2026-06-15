package gitops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	got := SortedKinds(map[string]int{"B": 1, "A": 3, "C": 1})
	want := []string{"A", "B", "C"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SortedKinds = %v, want %v", got, want)
		}
	}
}

func TestValidateManifests_NoFluxContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plain.yaml"), "foo: bar\n")
	verdicts, meta, err := ValidateManifests(context.Background(), dir, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(verdicts) != 0 {
		t.Fatalf("expected no verdicts for non-Flux repo, got %v", verdicts)
	}
	if meta.Roots != 0 {
		t.Fatalf("expected 0 roots, got %d", meta.Roots)
	}
}

// TestGraphVerdicts_Attribution exercises the per-Kustomization attribution of
// graph-level proofs directly (no disk, no kustomize/kubeconform): cycle members
// and dangling-dep referrers each get a Fail verdict; clean nodes Pass.
func TestGraphVerdicts_Attribution(t *testing.T) {
	// a <-> b cycle; c is clean; d dangles on a missing 'ghost'.
	g := mkGraph(node("a", "a", "b"), node("b", "b", "a"), node("c", "c"), node("d", "d", "ghost"))
	v := graphVerdicts(g)

	if v[k("a")].Status != Fail {
		t.Errorf("a should Fail (cycle), got %v", v[k("a")].Status)
	}
	if v[k("b")].Status != Fail {
		t.Errorf("b should Fail (cycle), got %v", v[k("b")].Status)
	}
	if v[k("c")].Status != Pass {
		t.Errorf("c should Pass, got %v (%v)", v[k("c")].Status, v[k("c")].Reasons)
	}
	d := v[k("d")]
	if d.Status != Fail {
		t.Errorf("d should Fail (dangling dep), got %v", d.Status)
	}
	if !strings.Contains(strings.Join(d.Reasons, " "), "ghost") {
		t.Errorf("d's reason should name the dangling dep 'ghost', got %v", d.Reasons)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
