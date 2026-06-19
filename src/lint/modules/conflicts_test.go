package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func runConflicts(t *testing.T, content string) []lint.Finding {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := (&conflictsModule{}).Check(context.Background(), lint.FileInfo{Path: "f.txt", AbsPath: p})
	if err != nil {
		t.Fatal(err)
	}
	return findings
}

// The regression that motivated this: markdown setext headers and horizontal rules
// begin with "=======" and were flagged as merge conflicts. They must stay clean.
func TestConflicts_MarkdownNotFlagged(t *testing.T) {
	md := "Inventory scripts for Jetporch\n" +
		"==============================\n\n" + // long underline
		"About This Repo\n" +
		"===============\n\n" + // shorter underline
		"Example\n" +
		"=======\n" // exactly 7 — still a header, not a conflict
	if f := runConflicts(t, md); len(f) != 0 {
		t.Fatalf("markdown headers flagged as conflicts: %+v", f)
	}
}

func TestConflicts_RealConflictFlagged(t *testing.T) {
	src := "before\n" +
		"<<<<<<< HEAD\n" +
		"ours\n" +
		"=======\n" +
		"theirs\n" +
		">>>>>>> branch\n" +
		"after\n"
	if f := runConflicts(t, src); len(f) != 3 {
		t.Fatalf("real conflict: want 3 markers, got %d: %+v", len(f), f)
	}
}

func TestConflicts_StrayMarkers(t *testing.T) {
	// Openers/closers are never decorative — flag even when unmatched.
	if f := runConflicts(t, "<<<<<<< HEAD\n"); len(f) != 1 {
		t.Fatalf("stray opener: want 1, got %d", len(f))
	}
	if f := runConflicts(t, ">>>>>>>\n"); len(f) != 1 {
		t.Fatalf("stray closer: want 1, got %d", len(f))
	}
	// A lone separator with no surrounding conflict is NOT a marker.
	if f := runConflicts(t, "=======\n"); len(f) != 0 {
		t.Fatalf("lone separator flagged: %+v", f)
	}
}

func TestIsConflictMarker(t *testing.T) {
	cases := []struct {
		in   string
		ch   byte
		want bool
	}{
		{"=======", '=', true},
		{"<<<<<<<", '<', true},
		{">>>>>>>", '>', true},
		{"<<<<<<< HEAD", '<', true},
		{">>>>>>> feature/x", '>', true},
		{"========================", '=', false}, // decorative rule
		{"===============", '=', false},          // markdown underline
		{"======", '=', false},                   // only six
		{"=======x", '=', false},                 // seven then non-space
	}
	for _, c := range cases {
		if got := isConflictMarker(c.in, c.ch); got != c.want {
			t.Errorf("isConflictMarker(%q,%q)=%v want %v", c.in, string(c.ch), got, c.want)
		}
	}
}
