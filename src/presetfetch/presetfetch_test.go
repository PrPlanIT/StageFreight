package presetfetch

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// TestClassify covers the branch-vs-tag resolution from ls-remote output: a branch (or
// both) → Tracked; a tag-only name → Pinned; neither → error.
func TestClassify(t *testing.T) {
	cases := []struct {
		name           string
		branch, tag    bool
		want           presetref.Kind
		wantErr        bool
	}{
		{"branch", true, false, presetref.Tracked, false},
		{"tag-only", false, true, presetref.Pinned, false},
		{"both-prefers-branch", true, true, presetref.Tracked, false},
		{"neither", false, false, presetref.Named, true},
	}
	for _, c := range cases {
		c := c
		g := &gitFetcher{
			resolve:   func(s string) (string, error) { return "https://example/repo", nil },
			refExists: func(url, ref string) (bool, bool, error) { return c.branch, c.tag, nil },
		}
		got, err := g.Classify("gitlab:Org/Repo", "someref")
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("%s: Classify = %s, want %s", c.name, got, c.want)
		}
	}
}

// TestNew_ReturnsFetcher confirms the constructor yields a usable presetref.Fetcher.
func TestNew_ReturnsFetcher(t *testing.T) {
	var _ presetref.Fetcher = New(func(s string) (string, error) { return s, nil })
}
