package cmd

import (
	"context"
	"fmt"
	"strings"

	git "github.com/go-git/go-git/v5"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/release"
	"github.com/PrPlanIT/StageFreight/src/upstream"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

// autoReleaseUpstream is upstream.release: auto. After a successful default-branch run in
// track mode, when the claimed source's pins name a version upstream has released and no tag
// of ours names it yet, StageFreight tags the commit it just built with that version and
// pushes the tag. The release pipeline then runs every version guard and evidence probe like
// any hand-cut release. It never fails publish: a tag it cannot cut is reported.
func autoReleaseUpstream(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, rootDir string) {
	u := appCfg.Upstream
	if !u.AutoRelease() || !acceptedState(ciCtx) || ciCtx.SHA == "" {
		return
	}
	_, claimedID := u.Version.Mode()
	sources, err := upstream.Sources(u)
	if err != nil {
		return
	}
	for i, cs := range u.Sources {
		synced := u.Sync.From == cs.ID && u.Sync.Onto.Branch == ""
		if cs.ID != claimedID || (len(cs.Pins) == 0 && !synced) {
			continue
		}
		src := sources[i]
		tag, reason := autoReleaseTag(ctx, rootDir, cs, src, ciCtx.SHA)
		if tag == "" {
			if reason != "" {
				fmt.Printf("  upstream: no release cut — %s\n", reason)
			}
			return
		}
		msg := fmt.Sprintf("- Upstream %s released %s", src.ID, strings.TrimPrefix(tag, "v"))
		if err := release.CreateAnnotatedTag(rootDir, tag, ciCtx.SHA, msg); err != nil {
			fmt.Printf("  upstream: release %s not cut: %v\n", tag, err)
			return
		}
		if err := release.PushTag(rootDir, "origin", tag, appCfg); err != nil {
			fmt.Printf("  upstream: release %s created but not pushed: %v\n", tag, err)
			return
		}
		fmt.Printf("  upstream: released %s on %s (upstream %s)\n", tag, ciCtx.SHA[:min(9, len(ciCtx.SHA))], src.ID)
		return
	}
}

// autoReleaseTag decides the tag to cut: "" with a reason when there is nothing to release.
// The version is what the pins hold, or — for a history fork synced onto upstream's
// releases — the newest upstream release whose commit the built commit contains.
func autoReleaseTag(ctx context.Context, rootDir string, cs config.UpstreamSource, src upstream.Source, built string) (tag, reason string) {
	l := listUpstream(ctx, src)
	if l.err != nil {
		return "", fmt.Sprintf("%s not listed: %v", src, l.err)
	}
	repo, err := gitstate.OpenRepo(rootDir)
	if err != nil {
		return "", err.Error()
	}
	var rel upstream.Release
	var current scheme.Version
	if len(cs.Pins) > 0 {
		pins, v, err := readPins(rootDir, cs, src.Select.Scheme)
		if err != nil {
			return "", err.Error()
		}
		r, ok := upstream.Find(l.releases, v)
		if !ok {
			return "", fmt.Sprintf("pins name %s, which %s has not released", pins[0].Value, src)
		}
		rel, current = r, v
	} else {
		r, ok := newestContained(repo, l.releases, built)
		if !ok {
			return "", fmt.Sprintf("the built commit contains no release of %s", src)
		}
		rel, current = r, r.Version
	}
	ours, _ := gitstate.ListTagsSorted(repo)
	vPrefix := true
	var newest string
	for _, t := range ours {
		v, ok := upstream.OurVersion(t, src.Select.Scheme)
		if !ok {
			continue
		}
		if v.Compare(current) == 0 {
			return "", "" // already released (a rebuild of it is the normal in-place rebuild)
		}
		if newest == "" {
			newest = t
		}
	}
	if newest != "" {
		vPrefix = strings.HasPrefix(newest, "v")
	}
	tag = strings.TrimPrefix(rel.Raw, "v")
	if vPrefix {
		tag = "v" + tag
	}
	return tag, ""
}

// newestContained is the newest release whose commit is an ancestor of (or is) built.
func newestContained(repo *git.Repository, releases []upstream.Release, built string) (upstream.Release, bool) {
	for _, r := range releases {
		if r.Commit == "" {
			continue
		}
		if ok, err := gitstate.IsAncestor(repo, r.Commit, built); err == nil && ok {
			return r, true
		}
	}
	return upstream.Release{}, false
}
