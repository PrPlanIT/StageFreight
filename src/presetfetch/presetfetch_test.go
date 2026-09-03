package presetfetch

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// TestClassify covers the branch-vs-tag resolution from ls-remote output: a branch (or
// both) → Tracked; a tag-only name → Pinned; neither → error.
func TestClassify(t *testing.T) {
	cases := []struct {
		name        string
		branch, tag bool
		want        presetref.Kind
		wantErr     bool
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
		got, err := g.Classify("gitlab:Org/Repo", "someref", "")
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

// recordFetcher notes which family a call landed in.
type recordFetcher struct {
	name string
	seen *[]string
}

func (r recordFetcher) Fetch(_, _, _ string) ([]byte, error) {
	*r.seen = append(*r.seen, r.name+":fetch")
	return nil, nil
}

func (r recordFetcher) Classify(_, _, _ string) (presetref.Kind, error) {
	*r.seen = append(*r.seen, r.name+":classify")
	return presetref.Tracked, nil
}

// A git repo is very often addressed by an https URL, so the scheme cannot decide the
// family — only the path can: a bare document URL IS the whole reference and carries
// none, while a repository reference always names a file inside it. Every entry point
// must route on that same pair.
func TestClassifyRoutesOnPathNotScheme(t *testing.T) {
	for _, c := range []struct {
		name, source, path, want string
	}{
		{"https git repo with a path", "https://gitlab.example.com/Org/Repo", "preset/lint.yml", "git"},
		{"bare document URL", "https://example.org/lint.yml", "", "http"},
		{"forge shorthand", "gitlab:Org/Repo", "preset/lint.yml", "git"},
	} {
		var seen []string
		d := &dispatchFetcher{
			git:  recordFetcher{name: "git", seen: &seen},
			http: recordFetcher{name: "http", seen: &seen},
		}
		if _, err := d.Classify(c.source, "", c.path); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		want := []string{c.want + ":classify"}
		if len(seen) != 1 || seen[0] != want[0] {
			t.Errorf("%s: routed %v, want %v", c.name, seen, want)
		}
	}
}
