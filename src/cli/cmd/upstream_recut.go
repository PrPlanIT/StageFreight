package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/release"
	"github.com/PrPlanIT/StageFreight/src/upstream"
)

// tagForge is the slice of a forge recutRelease needs: replacing a protected tag is an API
// call on every forge, never a force push.
type tagForge interface {
	DeleteTag(ctx context.Context, tagName string) error
	CreateTag(ctx context.Context, tagName, ref string) error
}

// recutRelease is upstream.recut: on_change. The version is already released at tag; when the
// commit just built descends from the tagged commit and its build inputs differ, the tag moves
// to it and the release pipeline republishes the version in place.
func recutRelease(ctx context.Context, appCfg *config.Config, rootDir string, sec *output.Section, tag, built string) {
	plan, reason := recutPlan(rootDir, appCfg.Builds, tag, built)
	if plan == nil {
		sec.Row("%-14s%s", "release", "none — "+reason)
		return
	}
	provider, clientURL := resolveForgeProvider(config.PrimaryURL(appCfg), appCfg)
	fc, err := newForgeClient(provider, clientURL)
	if err != nil {
		recutBlocked(rootDir, sec, tag, "no forge client for the primary: "+err.Error())
		return
	}
	push := func() error { return release.PushTag(rootDir, "origin", tag, appCfg) }
	if err := moveTag(ctx, fc, rootDir, tag, plan, push); err != nil {
		recutBlocked(rootDir, sec, tag, err.Error())
		return
	}
	sec.Row("%-14s%s %s recut on %s (was %s): the build's inputs changed", "release",
		output.StatusIcon("success", output.UseColor()), tag, short(plan.to), short(plan.from))
}

type recut struct{ from, to string }

// recutPlan decides whether tag moves to built: nil with the reason when it stays.
func recutPlan(rootDir string, builds []config.BuildConfig, tag, built string) (*recut, string) {
	from, err := release.PeelTag(rootDir, tag)
	if err != nil {
		return nil, err.Error()
	}
	if from == built {
		return nil, "already released"
	}
	repo, err := gitstate.OpenRepo(rootDir)
	if err != nil {
		return nil, err.Error()
	}
	if ok, err := gitstate.IsAncestor(repo, from, built); err != nil || !ok {
		return nil, fmt.Sprintf("%s is at %s, which this build does not descend from", tag, short(from))
	}
	was, err := buildInputsDigest(repo, plumbing.NewHash(from), builds)
	if err != nil {
		return nil, err.Error()
	}
	now, err := buildInputsDigest(repo, plumbing.NewHash(built), builds)
	if err != nil {
		return nil, err.Error()
	}
	if was == now {
		return nil, fmt.Sprintf("already released (%s ships the same build inputs)", tag)
	}
	return &recut{from: from, to: built}, ""
}

// moveTag replaces the remote tag, then pushes the annotated tag at its new commit. A push
// that fails leaves the tag at the new commit as the forge's lightweight tag, or failing
// that back at the old one: the version is never left without a tag.
func moveTag(ctx context.Context, fc tagForge, rootDir, tag string, plan *recut, push func() error) error {
	msg := fmt.Sprintf("- Recut %s on %s: the build's inputs changed", strings.TrimPrefix(tag, "v"), short(plan.to))
	if err := release.RecutAnnotatedTag(rootDir, tag, plan.to, msg); err != nil {
		return err
	}
	if err := fc.DeleteTag(ctx, tag); err != nil {
		return fmt.Errorf("the forge refused to remove %s: %w", tag, err)
	}
	perr := push()
	if perr == nil {
		return nil
	}
	if err := fc.CreateTag(ctx, tag, plan.to); err == nil {
		fmt.Fprintf(os.Stderr, "warning: %s recut as a lightweight tag: pushing the annotated tag failed: %v\n", tag, perr)
		return nil
	}
	if err := fc.CreateTag(ctx, tag, plan.from); err != nil {
		return fmt.Errorf("pushing %s failed (%v) and restoring it at %s failed (%v): the version has no tag", tag, perr, short(plan.from), err)
	}
	return fmt.Errorf("pushing %s failed, so it was restored at %s: %w", tag, short(plan.from), perr)
}

// recutBlocked reports a recut that did not happen and raises recut-blocked.
func recutBlocked(rootDir string, sec *output.Section, tag, why string) {
	sec.Row("%-14s%s %s not recut: %s", "release", output.StatusIcon("failed", output.UseColor()), tag, why)
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.MergeSubsystemResults("recut", map[string]string{cistate.SignalsKey: config.SignalRecutBlocked})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}
}

// recutTags are our version tags of a recut line: the primary moves them, so a mirror takes
// them by force like a rolling alias. Empty unless upstream.recut is in force.
func recutTags(appCfg *config.Config, rootDir string) map[string]bool {
	set := map[string]bool{}
	u := appCfg.Upstream
	if !u.AutoRecut() {
		return set
	}
	_, claimedID := u.Version.Mode()
	sources, err := upstream.Sources(u, rootDir)
	if err != nil {
		return set
	}
	repo, err := gitstate.OpenRepo(rootDir)
	if err != nil {
		return set
	}
	tags, _ := gitstate.ListTagsSorted(repo)
	for i, cs := range u.Sources {
		if cs.ID != claimedID {
			continue
		}
		for _, t := range tags {
			if _, ok := upstream.OurVersion(t, sources[i].Select.Scheme); ok {
				set[t] = true
			}
		}
	}
	return set
}
