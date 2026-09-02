package release

import (
	"fmt"
	"strings"
	"testing"
)

// Distinct summaries: sectionChanges dedups identical (scope, summary, author) triples,
// so repeating one commit would collapse to a single entry and exercise no bound.
func commitsN(n int) []Commit {
	out := make([]Commit, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Commit{
			Hash:    "abc1234",
			Summary: fmt.Sprintf("change %d that took some words to describe", i),
			Author:  "someone",
		})
	}
	return out
}

// The bound must land between entries. A reader seeing a shortened list understands it;
// a reader seeing a severed line assumes the notes are broken.
func TestSectionChangelog_StopsOnEntryBoundary(t *testing.T) {
	body := sectionChangelog(commitsN(500), 1000)

	if len(body) > 1200 { // the bound plus the omission line
		t.Errorf("section is %d chars, well past its bound", len(body))
	}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line == "" || strings.HasPrefix(line, "...") {
			continue
		}
		if !strings.HasPrefix(line, "- [`") || !strings.HasSuffix(line, ")") {
			t.Errorf("line is not a whole entry: %q", line)
		}
	}
	if !strings.Contains(body, "more commits") {
		t.Error("the omitted remainder must be stated")
	}
}

// Unbounded stays unbounded: the caller opts out with 0 and gets every entry.
func TestSectionChangelog_ZeroIsUnbounded(t *testing.T) {
	body := sectionChangelog(commitsN(500), 0)
	if got := strings.Count(body, "- [`abc1234`]"); got != 500 {
		t.Errorf("wrote %d entries, want all 500", got)
	}
	if strings.Contains(body, "more commits") {
		t.Error("nothing was omitted, so nothing should be claimed omitted")
	}
}

// A single entry longer than the bound is still written whole rather than cut: an entry
// is the unit, and half of one is not a smaller entry.
func TestSectionChangelog_NeverCutsASingleEntry(t *testing.T) {
	body := sectionChangelog(commitsN(3), 10)
	first := strings.SplitN(strings.TrimSpace(body), "\n", 2)[0]
	if !strings.HasSuffix(first, "(someone)") {
		t.Errorf("first entry was cut: %q", first)
	}
}

func TestSectionChanges_StopsOnEntryBoundary(t *testing.T) {
	cats := []CommitCategory{{Title: "Features", Commits: commitsN(200)}}
	body := sectionChanges(cats, 800)

	if len(body) > 1100 {
		t.Errorf("section is %d chars, past its bound", len(body))
	}
	if !strings.Contains(body, "more changes") {
		t.Error("the omitted remainder must be stated")
	}
}

// Every category is still headed and every remaining entry still considered: dropping
// the rest of the section once the budget ran out would hide categories entirely, and a
// reader cannot tell a category that had nothing from one that was silently skipped.
func TestSectionChanges_KeepsAllCategoriesVisible(t *testing.T) {
	cats := []CommitCategory{
		{Title: "Features", Commits: commitsN(200)},
		{Title: "Fixes", Commits: commitsN(3)},
	}
	body := sectionChanges(cats, 800)

	for _, title := range []string{"#### Features", "#### Fixes"} {
		if !strings.Contains(body, title) {
			t.Errorf("category heading missing: %q", title)
		}
	}
}
