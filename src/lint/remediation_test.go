package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyRemediations(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("foo  \nbaz"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := []Finding{
		{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 3, End: 5, Expected: "  "}},
		{File: "x.txt", Fix: &Remediation{Kind: "final-newline", Start: 9, End: 9, Expected: "", Replacement: "\n"}},
		{File: "x.txt", Message: "not fixable"}, // nil Fix → ignored
	}
	enabled := map[string]bool{"trailing-whitespace": true, "final-newline": true}
	sum, err := ApplyRemediations(findings, dir, enabled, false)
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(p); string(out) != "foo\nbaz\n" {
		t.Errorf("content = %q, want %q", out, "foo\nbaz\n")
	}
	if sum.FilesChanged != 1 || sum.EditsApplied != 2 {
		t.Errorf("summary = %+v, want 1 file / 2 edits", sum)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sf-fix-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestApplyRemediations_DisabledKindSkipped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	os.WriteFile(p, []byte("foo  \n"), 0o644)
	findings := []Finding{{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 3, End: 5, Expected: "  "}}}
	sum, err := ApplyRemediations(findings, dir, map[string]bool{"trailing-whitespace": false}, false)
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(p); string(out) != "foo  \n" {
		t.Errorf("disabled kind must not mutate; got %q", out)
	}
	if sum.EditsApplied != 0 {
		t.Errorf("EditsApplied = %d, want 0", sum.EditsApplied)
	}
}

// Transactional on drift: if ANY edit's Expected no longer matches, the WHOLE file is
// left untouched — never partially remediated into a mixed state.
func TestApplyRemediations_DriftTransactional(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	os.WriteFile(p, []byte("foo  \nbar"), 0o644)
	findings := []Finding{
		{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 3, End: 5, Expected: "  "}}, // valid
		{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 6, End: 8, Expected: "XX"}}, // drifted (bytes are "ba")
	}
	sum, err := ApplyRemediations(findings, dir, map[string]bool{"trailing-whitespace": true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(p); string(out) != "foo  \nbar" {
		t.Errorf("drift must skip the whole file (no partial apply); got %q", out)
	}
	if sum.Drifted != 1 || sum.EditsApplied != 0 {
		t.Errorf("want Drifted=1 EditsApplied=0, got %+v", sum)
	}
}

// Dry-run counts what WOULD change but writes nothing.
func TestApplyRemediations_DryRun(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	os.WriteFile(p, []byte("foo  \n"), 0o644)
	findings := []Finding{{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 3, End: 5, Expected: "  "}}}
	sum, err := ApplyRemediations(findings, dir, map[string]bool{"trailing-whitespace": true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(p); string(out) != "foo  \n" {
		t.Errorf("dry-run must not write; got %q", out)
	}
	if sum.EditsApplied != 1 || sum.FilesChanged != 1 {
		t.Errorf("dry-run should count what would change; got %+v", sum)
	}
}
