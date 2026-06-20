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
	if err := os.WriteFile(p, []byte("foo  \nbar\t\nbaz"), 0o644); err != nil {
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
	if _, err := lint.ApplyRemediations(got, dir, map[string]bool{"trailing-whitespace": true, "final-newline": true}, false); err != nil {
		t.Fatal(err)
	}
	if out, _ := os.ReadFile(p); string(out) != "foo\nbar\nbaz\n" {
		t.Errorf("fixed content = %q, want %q", out, "foo\nbar\nbaz\n")
	}
}

// Golden boundary for Markdown, where trailing whitespace is semantically loaded.
// CommonMark: TWO OR MORE trailing spaces = a hard line break — so long runs are hard
// breaks too, not "accidental." We report trailing whitespace in .md but emit NO Fix for
// any of it; only the always-safe missing-final-newline carries a Fix.
func TestLineEndingsFix_MarkdownBoundary(t *testing.T) {
	dir := t.TempDir()
	le := &lineEndingsModule{}
	check := func(name, content string) []lint.Finding {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		got, _ := le.Check(context.Background(), lint.FileInfo{Path: name, AbsPath: p, Content: lint.Content{Kind: lint.ContentText}})
		return got
	}
	probe := func(fs []lint.Finding, msg string) (found, hasFix bool) {
		for _, f := range fs {
			if f.Message == msg {
				return true, f.Fix != nil
			}
		}
		return false, false
	}

	cases := []struct{ name, file, content string }{
		{"two-space-hard-break", "a.md", "line with break  \nnext\n"},
		{"long-space-run-still-hard-break", "b.md", "line     \nnext\n"},
		{"fenced-code-trailing-ws", "c.md", "```\ncode  \n```\n"},
	}
	for _, c := range cases {
		found, hasFix := probe(check(c.file, c.content), "trailing whitespace")
		if !found {
			t.Errorf("%s: expected a trailing-whitespace finding", c.name)
		}
		if hasFix {
			t.Errorf("%s: markdown trailing whitespace must NOT carry a Fix", c.name)
		}
	}

	// Missing final newline IS safe in Markdown → Fix present.
	if found, hasFix := probe(check("d.md", "text no newline"), "missing final newline"); !found || !hasFix {
		t.Errorf("md missing-final-newline: found=%v hasFix=%v; want both true", found, hasFix)
	}
}
