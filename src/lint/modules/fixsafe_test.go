package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// End-to-end: lineendings emits byte-exact Fixes, the applier produces clean content.
func TestLineEndingsFix_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte("foo  \nbar\t\nbaz"), 0o644); err != nil { // ws line1+2, no final NL
		t.Fatal(err)
	}
	fi := lint.FileInfo{Path: "x.go", AbsPath: p, Content: lint.Content{Kind: lint.ContentText}}
	got, err := (&lineEndingsModule{}).Check(context.Background(), fi)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f.Fix == nil {
			t.Errorf("authored .go: finding %q should carry a Fix", f.Message)
		}
	}
	if _, err := lint.ApplyRemediations(got, dir, map[string]bool{"trailing-whitespace": true, "final-newline": true}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	if string(out) != "foo\nbar\nbaz\n" {
		t.Errorf("fixed content = %q, want %q", out, "foo\nbar\nbaz\n")
	}
}

// Markdown trailing whitespace is reported but carries NO Fix (2+ spaces = hard break).
func TestLineEndingsFix_MarkdownWithheld(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.md")
	os.WriteFile(p, []byte("a line with break  \nnext\n"), 0o644)
	fi := lint.FileInfo{Path: "x.md", AbsPath: p, Content: lint.Content{Kind: lint.ContentText}}
	got, _ := (&lineEndingsModule{}).Check(context.Background(), fi)
	found := false
	for _, f := range got {
		if f.Message == "trailing whitespace" {
			found = true
			if f.Fix != nil {
				t.Error("markdown trailing whitespace must NOT carry a Fix (hard-break risk)")
			}
		}
	}
	if !found {
		t.Error("expected a trailing-whitespace finding on .md")
	}
}
