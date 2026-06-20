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
	if err := os.WriteFile(p, []byte("foo  \nbaz"), 0o644); err != nil { // 2 trailing spaces, no final NL
		t.Fatal(err)
	}
	findings := []Finding{
		{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 3, End: 5, Expected: "  "}},
		{File: "x.txt", Fix: &Remediation{Kind: "final-newline", Start: 9, End: 9, Expected: "", Replacement: "\n"}},
		{File: "x.txt", Message: "not fixable"}, // nil Fix → ignored
	}
	enabled := map[string]bool{"trailing-whitespace": true, "final-newline": true}
	sum, err := ApplyRemediations(findings, dir, enabled)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	if string(out) != "foo\nbaz\n" {
		t.Errorf("content = %q, want %q", out, "foo\nbaz\n")
	}
	if sum.FilesChanged != 1 || sum.EditsApplied != 2 {
		t.Errorf("summary = %+v, want 1 file / 2 edits", sum)
	}
	// Atomic write must leave no temp file behind.
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
	sum, err := ApplyRemediations(findings, dir, map[string]bool{"trailing-whitespace": false})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	if string(out) != "foo  \n" {
		t.Errorf("disabled kind must not mutate; got %q", out)
	}
	if sum.EditsApplied != 0 {
		t.Errorf("EditsApplied = %d, want 0", sum.EditsApplied)
	}
}

// Compare-and-swap: an edit whose Expected no longer matches the file is skipped as
// stale, never misapplied. Guards against races / replayed findings / edits between
// detect and fix.
func TestApplyRemediations_StaleSkipped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	os.WriteFile(p, []byte("hello world"), 0o644)
	findings := []Finding{
		{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 0, End: 5, Expected: "XXXXX"}},
	}
	sum, err := ApplyRemediations(findings, dir, map[string]bool{"trailing-whitespace": true})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	if string(out) != "hello world" {
		t.Errorf("stale CAS must not mutate; got %q", out)
	}
	if sum.Stale != 1 || sum.EditsApplied != 0 {
		t.Errorf("want Stale=1 EditsApplied=0, got %+v", sum)
	}
}
