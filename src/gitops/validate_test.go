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
		t.Errorf("c should Pass, got %v (%v)", v[k("c")].Status, v[k("c")].Findings)
	}
	d := v[k("d")]
	if d.Status != Fail {
		t.Errorf("d should Fail (dangling dep), got %v", d.Status)
	}
	var dmsgs []string
	for _, f := range d.Findings {
		dmsgs = append(dmsgs, f.Message)
		if f.Source != "graph" {
			t.Errorf("d's finding source should be 'graph', got %q", f.Source)
		}
	}
	if !strings.Contains(strings.Join(dmsgs, " "), "ghost") {
		t.Errorf("d's reason should name the dangling dep 'ghost', got %v", d.Findings)
	}
}

// concatManifests must never emit empty/kind-less documents — a separator before
// each file (or a file's own leading "---") used to produce phantom "missing
// 'kind' key" failures for raw-manifest Flux roots (no kustomization.yaml).
func TestConcatManifests_NoEmptyDocs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")
	writeFile(t, filepath.Join(dir, "b.yaml"), "apiVersion: v1\nkind: Secret\nmetadata:\n  name: b\n")
	writeFile(t, filepath.Join(dir, "empty.yaml"), "\n---\n  \n") // separator/whitespace only
	writeFile(t, filepath.Join(dir, "ignore.txt"), "not yaml")

	out, err := concatManifests(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.HasPrefix(s, "---") || strings.HasPrefix(s, "\n") {
		t.Errorf("output must not start with a separator (would be an empty doc): %q", s)
	}
	for _, doc := range strings.Split(s, "\n---\n") {
		if strings.TrimSpace(doc) == "" {
			t.Errorf("empty document in concat output:\n%q", s)
		}
		if !strings.Contains(doc, "kind:") {
			t.Errorf("document missing kind:\n%q", doc)
		}
	}
	if !strings.Contains(s, "name: a") || !strings.Contains(s, "name: b") {
		t.Errorf("real documents were dropped: %q", s)
	}
}

// add raises a verdict to the highest finding severity and never lowers it: an
// advisory Warn must not mask an authoritative Fail.
func TestVerdictAdd_SeverityMonotonic(t *testing.T) {
	var v Verdict
	v.warn("crd-catalog", "advisory")
	if v.Status != Warn {
		t.Fatalf("warn should raise Pass→Warn, got %v", v.Status)
	}
	v.fail("core-schema", "authoritative")
	if v.Status != Fail {
		t.Fatalf("fail should raise Warn→Fail, got %v", v.Status)
	}
	v.warn("crd-catalog", "more advisory")
	if v.Status != Fail {
		t.Fatalf("a later warn must not mask Fail, got %v", v.Status)
	}
	if len(v.Findings) != 3 {
		t.Fatalf("all findings retained, got %d", len(v.Findings))
	}
	if v.Findings[0].Source != "crd-catalog" || v.Findings[1].Source != "core-schema" {
		t.Errorf("provenance not preserved per finding: %+v", v.Findings)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
