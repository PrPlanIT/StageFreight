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

	masterminds "github.com/Masterminds/semver/v3"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// Source is one declared upstream, ready to list.
type Source struct {
	Kind     string // "git" | "image"
	Location string // repository URL or image repository
	Match    *regexp.Regexp
}

// String names the source for messages.
func (s Source) String() string { return s.Kind + " " + s.Location }

// Release is one upstream release: the tag as published and the version it names.
type Release struct {
	Tag     string
	Version *masterminds.Version
}

// Sources builds the listable sources from config, compiling each match pattern.
func Sources(u config.UpstreamConfig) ([]Source, error) {
	var out []Source
	add := func(kind, loc, match string) error {
		re, err := regexp.Compile(config.EffectiveMatch(match))
		if err != nil {
			return fmt.Errorf("upstream %s %s: match: %w", kind, loc, err)
		}
		out = append(out, Source{Kind: kind, Location: loc, Match: re})
		return nil
	}
	for _, g := range u.Git {
		if err := add("git", g.Repo, g.Match); err != nil {
			return nil, err
		}
	}
	for _, im := range u.Images {
		if err := add("image", im.Ref, im.Match); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListTags lists every tag the source publishes.
func ListTags(ctx context.Context, src Source) ([]string, error) {
	switch src.Kind {
	case "git":
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
	case "image":
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

// Releases keeps the tags the source's pattern selects, newest first. The version is the
// named group "version", else the first group, else the whole tag; tags whose version does
// not parse are dropped.
func Releases(tags []string, match *regexp.Regexp) []Release {
	var out []Release
	vi := match.SubexpIndex("version")
	for _, t := range tags {
		m := match.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		raw := m[0]
		switch {
		case vi > 0:
			raw = m[vi]
		case len(m) > 1:
			raw = m[1]
		}
		v := version.ParseVersion(raw)
		if v == nil {
			continue
		}
		out = append(out, Release{Tag: t, Version: v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Version.GreaterThan(out[j].Version) })
	return out
}

// OurVersion returns the upstream version one of our release tags names: the semver at the
// start of the tag, v-prefix and any "-suffix" (an image revision) stripped. ok is false for
// tags that name no version.
func OurVersion(tag string) (*masterminds.Version, bool) {
	dt := version.DecomposeTag(tag)
	if dt.Version == nil || dt.PreRank != 0 {
		return nil, false
	}
	base, err := masterminds.NewVersion(fmt.Sprintf("%d.%d.%d", dt.Version.Major(), dt.Version.Minor(), dt.Version.Patch()))
	if err != nil {
		return nil, false
	}
	return base, true
}

// Report is what the watcher found for one source.
type Report struct {
	Source           Source
	UpstreamNewest   *Release // nil when the source published nothing the pattern selects
	OursNewest       string   // our newest release tag, "" when we have none
	UpdatesAvailable bool     // upstream's newest is newer than our newest
	Mismatched       []string // (track) our release tags naming a version upstream never released
}

// Evaluate compares the upstream's releases with our release tags. ours is every tag in our
// repo; tags that name no version are ignored. Mismatched is filled only for track mode, where
// our version is upstream's version; in own mode our lineage is ours and nothing can mismatch.
func Evaluate(src Source, upstream []Release, ours []string, track bool) Report {
	r := Report{Source: src}
	if len(upstream) > 0 {
		r.UpstreamNewest = &upstream[0]
	}
	published := make(map[string]bool, len(upstream))
	for _, rel := range upstream {
		published[rel.Version.String()] = true
	}
	var newest *masterminds.Version
	for _, t := range ours {
		v, ok := OurVersion(t)
		if !ok {
			continue
		}
		if newest == nil || v.GreaterThan(newest) {
			newest, r.OursNewest = v, t
		}
		if track && len(upstream) > 0 && !published[v.String()] {
			r.Mismatched = append(r.Mismatched, t)
		}
	}
	sort.Strings(r.Mismatched)
	if r.UpstreamNewest != nil && (newest == nil || r.UpstreamNewest.Version.GreaterThan(newest)) {
		r.UpdatesAvailable = true
	}
	return r
}

// ReleaseVerdict decides whether a release tag may ship under version: track: its version must
// be one upstream published. ok=false carries the reason the release is refused.
func ReleaseVerdict(src Source, upstream []Release, tag string) (ok bool, reason string) {
	v, named := OurVersion(tag)
	if !named {
		return false, fmt.Sprintf("release tag %q names no version, but upstream.version is track", tag)
	}
	for _, rel := range upstream {
		if rel.Version.Equal(v) {
			return true, ""
		}
	}
	newest := "nothing"
	if len(upstream) > 0 {
		newest = upstream[0].Tag
	}
	return false, fmt.Sprintf("release %s names version %s, which %s has not released (newest: %s)", tag, v, src, newest)
}
