package lint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyRemediations(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("foo  \nbaz"), 0o644); err != nil { // 2 trailing spaces, no final NL
		t.Fatal(err)
	}
	findings := []Finding{
		{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 3, End: 5}},
		{File: "x.txt", Fix: &Remediation{Kind: "final-newline", Start: 9, End: 9, Replacement: "\n"}},
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
}

func TestApplyRemediations_DisabledKindSkipped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	os.WriteFile(p, []byte("foo  \n"), 0o644)
	findings := []Finding{{File: "x.txt", Fix: &Remediation{Kind: "trailing-whitespace", Start: 3, End: 5}}}
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
