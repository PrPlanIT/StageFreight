package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
	"github.com/PrPlanIT/StageFreight/src/upstream"
)

// upstreamListTimeout bounds one upstream listing; a slow registry must not stall audition.
const upstreamListTimeout = 60 * time.Second

// watchUpstream runs upstream mode's audition step for a repo that declares upstream:. It lists
// every source, records what it found as the "upstream" subsystem (facts {upstream.*} and the
// run's signals), and returns the reason a release must be refused, or "" when nothing is.
//
// Only a release under version: track or continue can be refused, and it fails closed: when the
// claimed source cannot be listed, a release whose claim cannot be verified does not ship. Any
// other listing failure degrades the run to a warning and raises upstream-unreachable.
func watchUpstream(ctx context.Context, appCfg *config.Config, rootDir string) (refuse string) {
	u := appCfg.Upstream
	if !u.Declared() {
		return ""
	}
	mode, claimed := u.Version.Mode()
	releaseTag := config.CITag()
	guarding := mode != config.UpstreamVersionOwn && releaseTag != ""

	sources, err := upstream.Sources(u)
	if err != nil {
		return recordUpstreamFailure(rootDir, guarding, err)
	}
	var ours []string
	if repo, err := gitstate.OpenRepo(rootDir); err == nil {
		ours, _ = gitstate.ListTagsSorted(repo)
	}

	var (
		signals  []string
		failures []string
		unparsed int
		facts    = map[string]string{}
	)
	for _, src := range sources {
		lctx, cancel := context.WithTimeout(ctx, upstreamListTimeout)
		tags, err := upstream.ListTags(lctx, src)
		cancel()
		if err != nil {
			if guarding && src.ID == claimed {
				return recordUpstreamFailure(rootDir, true, err)
			}
			fmt.Printf("  upstream: %s not listed: %v\n", src, err)
			failures = append(failures, src.ID)
			signals = appendOnce(signals, config.SignalUpstreamUnreachable)
			continue
		}
		releases, bad := upstream.Releases(tags, src.Select)
		unparsed += len(bad)
		if len(bad) > 0 {
			fmt.Printf("  upstream: %s: %d tag(s) selected but not a version, ignored: %s\n", src, len(bad), sample(bad, 5))
		}
		latest := "none"
		if len(releases) > 0 {
			latest = releases[0].Tag
		}
		facts[src.ID+".latest"] = latest
		fmt.Printf("  upstream: %s — newest %s\n", src, latest)

		if src.ID != claimed {
			continue
		}
		claim := upstream.Claim{Mode: mode, Source: src}
		if u.Version.Since != "" {
			claim.Since = version.ParseVersion(u.Version.Since)
		}
		rep := upstream.Evaluate(claim, releases, ours)
		facts["source"], facts["latest"], facts["ours"] = src.ID, latest, orNone(rep.OursNewest)
		if rep.UpdatesAvailable {
			fmt.Printf("  upstream: %s released %s, newer than ours (%s)\n", src, latest, orNone(rep.OursNewest))
			signals = appendOnce(signals, config.SignalUpdatesAvailable)
		}
		if len(rep.Mismatched) > 0 {
			fmt.Printf("  upstream: our tags make a false %s claim about %s: %s\n", mode, src, strings.Join(rep.Mismatched, ", "))
			signals = appendOnce(signals, config.SignalVersionMismatch)
			facts["mismatched"] = strings.Join(rep.Mismatched, ",")
		}
		if guarding {
			if ok, reason := upstream.ReleaseVerdict(claim, releases, releaseTag); !ok {
				refuse = reason
			}
		}
	}
	if unparsed > 0 {
		facts["unparsed"] = strconv.Itoa(unparsed)
	}
	facts[cistate.SignalsKey] = strings.Join(signals, ",")

	sub := cistate.SubsystemState{
		Name: "upstream", Attempted: true, Completed: true, AllowFailure: true,
		Outcome: "success", Reason: "upstream watched", Results: facts,
	}
	if len(failures) > 0 {
		sub.Outcome, sub.Reason = "warning", "upstream not listed: "+strings.Join(failures, ", ")
	}
	if refuse != "" {
		fmt.Printf("  upstream: refusing release — %s\n", refuse)
		sub.Outcome, sub.Reason, sub.AllowFailure = "failed", refuse, false
	}
	recordSubsystemOutcome(rootDir, sub)
	return refuse
}

// recordUpstreamFailure records a run in which upstream could not be watched. A claimed release
// cannot be verified without the listing, so it is refused; any other run only degrades to a
// warning.
func recordUpstreamFailure(rootDir string, guarding bool, err error) string {
	sub := cistate.SubsystemState{
		Name: "upstream", Attempted: true, Completed: true, AllowFailure: true,
		Outcome: "warning", Reason: fmt.Sprintf("upstream not watched: %v", err),
		Results: map[string]string{cistate.SignalsKey: config.SignalUpstreamUnreachable},
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

// sample renders up to n items, noting how many more there are.
func sample(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s, … (%d more)", strings.Join(items[:n], ", "), len(items)-n)
}
