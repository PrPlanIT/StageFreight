package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/promote"
	"github.com/PrPlanIT/StageFreight/src/upstream"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

// evidenceGate proves, before an image is distributed, that it contains the version the
// release claims (upstream.evidence). It applies only to a release tag whose claim is judged
// (track or continue, at or after since); dev builds claim no version.
type evidenceGate struct {
	claimed  scheme.Version
	tag      string
	scheme   scheme.Scheme
	evidence map[string]config.UpstreamEvidence

	checked    int
	mismatched []string
	reported   bool
}

// newEvidenceGate returns nil when nothing this run needs proving.
func newEvidenceGate(appCfg *config.Config) *evidenceGate {
	u := appCfg.Upstream
	tag := config.CITag()
	mode, claimedID := u.Version.Mode()
	if tag == "" || mode == config.UpstreamVersionOwn || len(u.Evidence) == 0 {
		return nil
	}
	sources, err := upstream.Sources(u)
	if err != nil {
		return nil
	}
	for _, src := range sources {
		if src.ID != claimedID {
			continue
		}
		v, ok := upstream.OurVersion(tag, src.Select.Scheme)
		if !ok {
			return nil
		}
		if u.Version.Since != "" {
			if since, err := src.Select.Scheme.Parse(u.Version.Since); err == nil && v.Compare(since) < 0 {
				return nil
			}
		}
		g := &evidenceGate{claimed: v, tag: tag, scheme: src.Select.Scheme, evidence: map[string]config.UpstreamEvidence{}}
		for _, e := range u.Evidence {
			g.evidence[e.ID] = e
		}
		return g
	}
	return nil
}

// check probes one built image. refuse is the reason it must not be distributed ("" when it
// may); a tolerated mismatch (on_mismatch: report) is printed and recorded, not refused.
func (g *evidenceGate) check(build, layoutDir, digest string) (refuse string) {
	if g == nil {
		return ""
	}
	e, ok := g.evidence[build]
	if !ok {
		return ""
	}
	g.checked++
	reason := g.prove(e, layoutDir, digest)
	if reason == "" {
		fmt.Printf("  evidence: %s contains %s, as %s claims\n", build, g.claimed, g.tag)
		return ""
	}
	g.mismatched = append(g.mismatched, build)
	if config.EffectiveOnMismatch(e.OnMismatch) == config.UpstreamReport {
		fmt.Printf("  evidence: %s — distributing anyway (on_mismatch: report — %s)\n", reason, e.Reason)
		g.reported = true
		return ""
	}
	return reason
}

func (g *evidenceGate) prove(e config.UpstreamEvidence, layoutDir, digest string) string {
	img, err := promote.LayoutImage(layoutDir, digest)
	if err != nil {
		return fmt.Sprintf("cannot prove %s contains %s: %v", e.ID, g.claimed, err)
	}
	raw, err := upstream.Probe(img, e.Probe)
	if err != nil {
		return fmt.Sprintf("cannot prove %s contains %s: %v", e.ID, g.claimed, err)
	}
	got, err := g.scheme.Parse(upstream.UpstreamPart(e.Probe.Manager, raw))
	if err != nil {
		return fmt.Sprintf("%s reports version %q, which is not a %s version (release %s claims %s)", e.ID, raw, g.scheme.Name(), g.tag, g.claimed)
	}
	if got.Compare(g.claimed) != 0 {
		return fmt.Sprintf("%s contains %s, but release %s claims %s — the image is mislabelled", e.ID, raw, g.tag, g.claimed)
	}
	return ""
}

// record writes the evidence subsystem: facts and the version-mismatch signal for a tolerated
// mismatch. A refused image fails publish through the distribution path.
func (g *evidenceGate) record(rootDir string) {
	if g == nil || g.checked == 0 {
		return
	}
	sub := cistate.SubsystemState{
		Name: "evidence", Attempted: true, Completed: true, AllowFailure: true,
		Outcome: "success", Reason: "release version proven",
		Results: map[string]string{"checked": strconv.Itoa(g.checked)},
	}
	if len(g.mismatched) > 0 {
		sub.Outcome, sub.Reason = "warning", "image version differs from the release claim: "+strings.Join(g.mismatched, ", ")
		sub.Results["mismatched"] = strings.Join(g.mismatched, ",")
	}
	if g.reported {
		sub.Results[cistate.SignalsKey] = config.SignalVersionMismatch
	}
	recordSubsystemOutcome(rootDir, sub)
}
