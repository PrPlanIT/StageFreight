package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/upstream"
)

// upstreamListTimeout bounds one upstream listing; a slow registry must not stall audition.
const upstreamListTimeout = 60 * time.Second

// watchUpstream runs fork mode's audition step for a repo that declares upstream:. It records
// what it found as the "upstream" subsystem (facts {upstream.*} and the run's signals) and
// returns the reason a release must be refused, or "" when nothing is refused.
//
// Only a release under version: track can be refused, and it fails closed: when upstream cannot
// be listed, a release whose version cannot be verified does not ship. Everywhere else the
// watcher only reports, and a failed listing degrades the run to a warning instead of blocking.
func watchUpstream(ctx context.Context, appCfg *config.Config, rootDir string) (refuse string) {
	u := appCfg.Upstream
	if !u.Declared() {
		return ""
	}
	track := u.VersionMode() == config.UpstreamVersionTrack
	releaseTag := config.CITag()
	guarding := track && releaseTag != ""

	sources, err := upstream.Sources(u)
	if err != nil {
		return recordUpstreamFailure(rootDir, guarding, err)
	}
	var ours []string
	if repo, err := gitstate.OpenRepo(rootDir); err == nil {
		ours, _ = gitstate.ListTagsSorted(repo)
	}

	var (
		signals    []string
		mismatched []string
		facts      = map[string]string{}
	)
	for _, src := range sources {
		lctx, cancel := context.WithTimeout(ctx, upstreamListTimeout)
		tags, err := upstream.ListTags(lctx, src)
		cancel()
		if err != nil {
			return recordUpstreamFailure(rootDir, guarding, err)
		}
		releases := upstream.Releases(tags, src.Match)
		rep := upstream.Evaluate(src, releases, ours, track)

		latest := "none"
		if rep.UpstreamNewest != nil {
			latest = rep.UpstreamNewest.Tag
		}
		fmt.Printf("  upstream: %s — newest %s, ours %s\n", src, latest, orNone(rep.OursNewest))
		if facts["latest"] == "" {
			facts["source"], facts["latest"], facts["ours"] = src.String(), latest, orNone(rep.OursNewest)
		}
		if rep.UpdatesAvailable {
			fmt.Printf("  upstream: %s released %s, newer than ours\n", src, latest)
			signals = appendOnce(signals, config.SignalUpdatesAvailable)
		}
		if len(rep.Mismatched) > 0 {
			fmt.Printf("  upstream: our tags name versions %s never released: %s\n", src, strings.Join(rep.Mismatched, ", "))
			signals = appendOnce(signals, config.SignalVersionMismatch)
			mismatched = append(mismatched, rep.Mismatched...)
		}
		if guarding {
			if ok, reason := upstream.ReleaseVerdict(src, releases, releaseTag); !ok {
				refuse = reason
			}
		}
	}

	facts["signals"] = strings.Join(signals, ",")
	facts["mismatched"] = strings.Join(mismatched, ",")
	sub := cistate.SubsystemState{
		Name: "upstream", Attempted: true, Completed: true, AllowFailure: true,
		Outcome: "success", Reason: "upstream watched", Results: facts,
	}
	if refuse != "" {
		fmt.Printf("  upstream: refusing release — %s\n", refuse)
		sub.Outcome, sub.Reason, sub.AllowFailure = "failed", refuse, false
	}
	recordSubsystemOutcome(rootDir, sub)
	return refuse
}

// recordUpstreamFailure records a failed upstream listing. A track-mode release cannot be
// verified without the listing, so it is refused; any other run only degrades to a warning.
func recordUpstreamFailure(rootDir string, guarding bool, err error) string {
	sub := cistate.SubsystemState{
		Name: "upstream", Attempted: true, Completed: true, AllowFailure: true,
		Outcome: "warning", Reason: fmt.Sprintf("upstream not watched: %v", err),
	}
	refuse := ""
	if guarding {
		refuse = fmt.Sprintf("cannot verify the release version against upstream: %v", err)
		sub.Outcome, sub.Reason, sub.AllowFailure = "failed", refuse, false
	}
	fmt.Printf("  upstream: %s\n", sub.Reason)
	recordSubsystemOutcome(rootDir, sub)
	return refuse
}

func appendOnce(list []string, v string) []string {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
