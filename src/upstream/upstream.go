// Package upstream answers fork mode's questions about a declared upstream: what has it
// released, is there something newer than ours, and does every release of ours name a version
// it actually published.
//
// Listing is anonymous by design: an upstream is somebody else's public project, so watching it
// needs no credentials (git ls-remote; the OCI registry API with its anonymous token flow).
package upstream

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

// Source is one declared upstream, ready to list.
type Source struct {
	ID       string
	Kind     string // config.UpstreamKind*
	Location string // repository URL or image repository
	Select   Selection
}

// Selection is a source's compiled select: block and scheme.
type Selection struct {
	Tags        *regexp.Regexp
	Exclude     []*regexp.Regexp
	Prereleases bool
	Rewrite     []Rewrite
	Scheme      scheme.Scheme
}

// Rewrite is one compiled select.rewrite step.
type Rewrite struct {
	From *regexp.Regexp
	To   string
}

// String names the source for messages.
func (s Source) String() string { return s.ID + " (" + s.Kind + " " + s.Location + ")" }

// Release is one upstream release: the tag as published and the version it names.
type Release struct {
	Tag     string
	Version scheme.Version
}

// Sources builds the listable sources from config, compiling each selection.
func Sources(u config.UpstreamConfig) ([]Source, error) {
	var out []Source
	for _, cs := range u.Sources {
		kind, loc := cs.Kind()
		sch, err := scheme.Get(cs.EffectiveScheme())
		if err != nil {
			return nil, fmt.Errorf("upstream.sources.%s.scheme: %w", cs.ID, err)
		}
		sel := Selection{Prereleases: cs.Select.Prereleases, Scheme: sch}
		for i, rw := range cs.Select.Rewrite {
			re, err := regexp.Compile(rw.From)
			if err != nil {
				return nil, fmt.Errorf("upstream.sources.%s.select.rewrite[%d].from: %w", cs.ID, i, err)
			}
			sel.Rewrite = append(sel.Rewrite, Rewrite{From: re, To: rw.To})
		}
		re, err := regexp.Compile(cs.Select.EffectiveTags())
		if err != nil {
			return nil, fmt.Errorf("upstream.sources.%s.select.tags: %w", cs.ID, err)
		}
		sel.Tags = re
		for i, ex := range cs.Select.Exclude {
			re, err := regexp.Compile(ex)
			if err != nil {
				return nil, fmt.Errorf("upstream.sources.%s.select.exclude[%d]: %w", cs.ID, i, err)
			}
			sel.Exclude = append(sel.Exclude, re)
		}
		out = append(out, Source{ID: cs.ID, Kind: kind, Location: loc, Select: sel})
	}
	return out, nil
}

// ListTags lists every tag the source publishes.
func ListTags(ctx context.Context, src Source) ([]string, error) {
	switch src.Kind {
	case config.UpstreamKindGit:
		rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "upstream", URLs: []string{src.Location}})
		refs, err := rem.ListContext(ctx, &git.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("ls-remote %s: %w", src.Location, err)
		}
		var tags []string
		for _, r := range refs {
			if r.Name().IsTag() {
				tags = append(tags, r.Name().Short())
			}
		}
		return tags, nil
	case config.UpstreamKindImage:
		repo, err := name.NewRepository(src.Location)
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", src.Location, err)
		}
		tags, err := remote.List(repo, remote.WithContext(ctx), remote.WithAuth(authn.Anonymous))
		if err != nil {
			return nil, fmt.Errorf("listing tags of %s: %w", src.Location, err)
		}
		return tags, nil
	default:
		return nil, fmt.Errorf("unknown upstream kind %q", src.Kind)
	}
}

// Releases applies the selection to a source's tags and returns its releases, newest first.
// The version is the named group "version" of select.tags, else its first group, else the whole
// tag. Candidates whose version does not parse are returned as unparsed, so the caller can
// report them instead of silently losing them.
func Releases(tags []string, sel Selection) (releases []Release, unparsed []string) {
	vi := sel.Tags.SubexpIndex("version")
tag:
	for _, t := range tags {
		m := sel.Tags.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		for _, ex := range sel.Exclude {
			if ex.MatchString(t) {
				continue tag
			}
		}
		raw := m[0]
		switch {
		case vi > 0:
			raw = m[vi]
		case len(m) > 1:
			raw = m[1]
		}
		for _, rw := range sel.Rewrite {
			raw = rw.From.ReplaceAllString(raw, rw.To)
		}
		v, err := sel.Scheme.Parse(raw)
		if err != nil {
			unparsed = append(unparsed, t)
			continue
		}
		if v.Prerelease() && !sel.Prereleases {
			continue
		}
		releases = append(releases, Release{Tag: t, Version: v})
	}
	// Equal versions (3.15 and 3.15.0) rank the more precise tag first, so a floating alias
	// never stands in for the release it points at.
	sort.SliceStable(releases, func(i, j int) bool {
		a, b := releases[i], releases[j]
		if c := a.Version.Compare(b.Version); c != 0 {
			return c > 0
		}
		if len(a.Tag) != len(b.Tag) {
			return len(a.Tag) > len(b.Tag)
		}
		return a.Tag < b.Tag
	})
	sort.Strings(unparsed)
	return releases, unparsed
}

// ourRevision is the revision suffix our own tags may carry on a rebuild of the same upstream
// version (v2.8.1-3, v0.9.2-r2). It is ours, not upstream's, so it is removed before the tag
// is read with the upstream's scheme.
var ourRevision = regexp.MustCompile(`-r?\d+$`)

// OurVersion returns the upstream version one of our release tags names, read with the
// claimed source's scheme after the v prefix and any revision suffix are removed. ok is false
// for tags that name no version.
func OurVersion(tag string, sch scheme.Scheme) (scheme.Version, bool) {
	v, err := sch.Parse(ourRevision.ReplaceAllString(strings.TrimPrefix(tag, "v"), ""))
	if err != nil {
		return nil, false
	}
	return v, true
}

// Claim is what our release tags claim about one upstream: the version mode, the source it
// names, and the cutover before which tags are not judged.
type Claim struct {
	Mode   string         // config.UpstreamVersionTrack | config.UpstreamVersionContinue
	Source Source         // the source the mode names
	Since  scheme.Version // nil: every tag is judged
}

// judged reports whether a tag's version falls under the claim (at or after the cutover).
func (c Claim) judged(v scheme.Version) bool {
	return c.Since == nil || v.Compare(c.Since) >= 0
}

// Report is what the watcher found for the claimed source.
type Report struct {
	UpstreamNewest   *Release // nil when the source released nothing the selection keeps
	OursNewest       string   // our newest release tag, "" when we have none
	UpdatesAvailable bool     // track: upstream's newest is newer than our newest
	Mismatched       []string // our judged tags whose claim is false
}

// Evaluate compares the claimed source's releases with our tags. Under track a tag is false
// when it names a version upstream never released; under continue, when it names one upstream
// has released. Tags naming no version, and tags before the cutover, are not judged.
func Evaluate(c Claim, upstream []Release, ours []string) Report {
	var r Report
	if len(upstream) > 0 {
		r.UpstreamNewest = &upstream[0]
	}
	var newest scheme.Version
	for _, t := range ours {
		v, ok := OurVersion(t, c.Source.Select.Scheme)
		if !ok {
			continue
		}
		if newest == nil || v.Compare(newest) > 0 {
			newest, r.OursNewest = v, t
		}
		if !c.judged(v) || len(upstream) == 0 {
			continue
		}
		if released(upstream, v) != (c.Mode == config.UpstreamVersionTrack) {
			r.Mismatched = append(r.Mismatched, t)
		}
	}
	sort.Strings(r.Mismatched)
	if c.Mode == config.UpstreamVersionTrack && r.UpstreamNewest != nil && (newest == nil || r.UpstreamNewest.Version.Compare(newest) > 0) {
		r.UpdatesAvailable = true
	}
	return r
}

// ReleaseVerdict decides whether a release tag may ship under the claim. ok=false carries the
// reason the release is refused.
func ReleaseVerdict(c Claim, upstream []Release, tag string) (ok bool, reason string) {
	v, named := OurVersion(tag, c.Source.Select.Scheme)
	if !named {
		if c.Mode == config.UpstreamVersionTrack {
			return false, fmt.Sprintf("release tag %q names no version, but upstream.version tracks %s", tag, c.Source.ID)
		}
		return true, ""
	}
	if !c.judged(v) {
		return true, ""
	}
	switch c.Mode {
	case config.UpstreamVersionTrack:
		if released(upstream, v) {
			return true, ""
		}
		newest := "nothing"
		if len(upstream) > 0 {
			newest = upstream[0].Tag
		}
		hint := ""
		if v.Prerelease() && !c.Source.Select.Prereleases {
			hint = "; prereleases are not selected (select.prereleases: true tracks them)"
		}
		return false, fmt.Sprintf("release %s names version %s, which %s has not released (newest: %s)%s", tag, v, c.Source, newest, hint)
	case config.UpstreamVersionContinue:
		if released(upstream, v) {
			return false, fmt.Sprintf("release %s names version %s, which %s has already released; our lineage continues its numbering and must not reuse it", tag, v, c.Source)
		}
	}
	return true, ""
}

func released(upstream []Release, v scheme.Version) bool {
	for _, rel := range upstream {
		if rel.Version.Compare(v) == 0 {
			return true
		}
	}
	return false
}
