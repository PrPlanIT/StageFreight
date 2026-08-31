// Package presetfetch is the concrete git Fetcher for source-tracking preset resolution.
// It satisfies presetref.Fetcher by cloning a source at a ref and reading the preset
// file (via governance.FetchFile), and by classifying a bare ref against the source with
// a go-git ls-remote (gitstate.RemoteRefExists — all git ops go through gitstate per the
// git-ops invariant). It is wired into config.SourceFetcher by a network-capable entry
// point (the CLI); config itself never imports it (that would cycle), hence the seam.
package presetfetch

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/governance"
	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// New returns a presetref.Fetcher covering both source families. resolveSource maps a
// preset ref's <source> (a URL, or a forge shorthand like "gitlab:Org/Repo") to a
// clonable repo URL; it is used only for the git family.
func New(resolveSource func(source string) (string, error)) presetref.Fetcher {
	return &dispatchFetcher{
		git:  &gitFetcher{resolve: resolveSource, refExists: gitstate.RemoteRefExists},
		http: newHTTPFetcher(),
	}
}

// dispatchFetcher routes a source to the family that can retrieve it.
//
// Scheme alone does not decide it: a git source is often an https URL too
// ("https://gitlab.example.com/Org/Repo"). What separates them is that a bare URL IS
// the whole reference, so Parse leaves its path empty, while a git reference always
// names a path within the repo.
type dispatchFetcher struct {
	git  presetref.Fetcher
	http presetref.Fetcher
}

func (d *dispatchFetcher) pick(source, path string) presetref.Fetcher {
	if path == "" && presetref.IsURL(source) {
		return d.http
	}
	return d.git
}

func (d *dispatchFetcher) Fetch(source, ref, path string) ([]byte, error) {
	return d.pick(source, path).Fetch(source, ref, path)
}

// Classify is only ever called for a bare Named ref, which a URL cannot carry — but
// route it consistently so the two families can never disagree about a source.
func (d *dispatchFetcher) Classify(source, ref string) (presetref.Kind, error) {
	return d.pick(source, "").Classify(source, ref)
}

type gitFetcher struct {
	resolve   func(string) (string, error)
	refExists func(url, ref string) (branch, tag bool, err error) // injectable for tests
}

func (g *gitFetcher) Fetch(source, ref, path string) ([]byte, error) {
	url, err := g.resolve(source)
	if err != nil {
		return nil, fmt.Errorf("resolving preset source %q: %w", source, err)
	}
	return governance.FetchFile(url, ref, path)
}

// Classify resolves a bare ref by asking the source which namespace it lives in. A
// branch (or a name that is both) → Tracked (tracking is the default); a name that is
// ONLY a tag → Pinned (honoring "a bare tag pins"); neither → an error.
func (g *gitFetcher) Classify(source, ref string) (presetref.Kind, error) {
	url, err := g.resolve(source)
	if err != nil {
		return presetref.Named, fmt.Errorf("resolving preset source %q: %w", source, err)
	}
	branch, tag, err := g.refExists(url, ref)
	if err != nil {
		return presetref.Named, err
	}
	switch {
	case branch:
		return presetref.Tracked, nil
	case tag:
		return presetref.Pinned, nil
	default:
		return presetref.Named, fmt.Errorf("ref %q not found as a branch or tag on %s", ref, url)
	}
}
