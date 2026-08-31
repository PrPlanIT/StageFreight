package render

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// Plan builds a forge-neutral Pipeline from StageFreight configuration.
//
// The pipeline graph is structurally stable across all lifecycle modes.
// Mode dispatch is the binary's job — the planner never branches the graph
// by lifecycle.mode. Review and publish exist for all modes; the runtime
// renders not_applicable where needed.
//
// Returns an error if ci.image is missing — no defaults, no guessing.
func Plan(cfg *config.Config) (model.Pipeline, error) {
	if cfg.CI.Image == "" {
		return model.Pipeline{}, fmt.Errorf("ci.image is required — set the StageFreight container image in .stagefreight.yml")
	}

	p := model.Pipeline{
		Defaults: model.PipelineDefaults{
			Image:            cfg.CI.Image,
			Interruptible:    true,
			CancelSuperseded: true,
			CIContext:        true,
			ReleaseTagRules:  releaseTagRules(cfg),
		},
		Jobs: []model.Job{
			{
				Name:     "audition",
				Stage:    "audition",
				Commands: []string{"stagefreight ci run audition"},
				Source:   model.SourceSpec{FullClone: true, Submodules: cfg.Git.Submodules},
				Artifacts: model.ArtifactSpec{
					Paths:    []string{paths.Root + "/"},
					ExpireIn: "1 week",
					// Deliver the audition CONTRACT even on a failed audition — perform gates on
					// the ledger, forge-agnostically, not on the forge dropping the artifact.
					WhenAlways: true,
				},
				Capabilities: model.CapabilitySpec{Docker: true, ForgeAPI: true},
				Policy:       model.PolicySpec{AllowFailure: true},
			},
			{
				Name:     "perform",
				Stage:    "perform",
				Commands: []string{"stagefreight ci run perform"},
				Source:   model.SourceSpec{FullClone: true, Submodules: cfg.Git.Submodules},
				Artifacts: model.ArtifactSpec{
					// perform's .stagefreight/ carries the content store
					// (objects/ = the built OCI layouts) that publish resolves and
					// promotes. The transport guarantee depends on these bytes
					// surviving until publish runs — which may be gated/queued/
					// retried hours after perform. 2h was tuned for manifests only
					// and is too tight once the layout rides along; 1 day covers a
					// realistic perform→publish gap including a manual publish gate.
					Paths:    []string{paths.Root + "/"},
					ExpireIn: "1 day",
				},
				Capabilities: model.CapabilitySpec{Docker: true, OIDC: true},
				// Reconcile/converge mutates live systems (flux, ansible
				// cordon/drain): concurrent pipelines must queue behind one
				// perform, never race it.
				Policy: model.PolicySpec{Serialize: "perform"},
			},
			{
				Name:     "review",
				Stage:    "review",
				Needs:    []string{"perform"},
				Commands: []string{"stagefreight ci run review"},
				Artifacts: model.ArtifactSpec{
					// Cross-job subsystem state travels as per-name fragments under
					// subsystems/ — the SINGLE carrier. review forwards only its own
					// fragment (subsystems/security.json) plus the scan reports under
					// security/; readers UNION the fragments (ReadState). review does
					// NOT forward pipeline.json: that is the local merged view, and
					// re-forwarding it was the old special case that made two jobs'
					// pipeline.json collide at publish (last-write-wins dropped review's
					// security outcome → "security did not run"). Fragments never
					// collide across jobs (perform→build.json, review→security.json), so
					// the union is order-independent. (review still does NOT re-forward
					// the whole .stagefreight/ — that would drag perform's content store
					// along with it.)
					Paths: []string{
						paths.Ephemeral("", "security") + "/",
						paths.Ephemeral("", "subsystems") + "/",
					},
					ExpireIn: "1 week",
				},
				Capabilities: model.CapabilitySpec{Docker: true},
				Policy:       model.PolicySpec{AllowFailure: true},
			},
			{
				Name:     "publish",
				Stage:    "publish",
				Needs:    []string{"perform", "review"},
				Commands: []string{"stagefreight ci run publish"},
				Source:   model.SourceSpec{FullClone: true, Submodules: cfg.Git.Submodules},
				Artifacts: model.ArtifactSpec{
					// publish forwards its own subsystem fragments (subsystems/
					// publish.json — the recorded tags/registries facts) the same way
					// review forwards security.json: fragments are the SINGLE cross-job
					// state carrier, and narrate unions them (ReadState). Without this
					// the publish facts died with the job and the summary's Shipped
					// line elided.
					Paths:    []string{paths.Ephemeral("", "subsystems") + "/"},
					ExpireIn: "1 week",
				},
				// OIDC: scribe's k8s-inventory region authenticates to the cluster via
				// the STAGEFREIGHT_OIDC id_token, same as perform's reconcile.
				Capabilities: model.CapabilitySpec{ForgeAPI: true, OIDC: true, PackageRegistries: packageRegistries(cfg)},
				Policy:       model.PolicySpec{AllowFailure: true},
			},
			{
				Name:  "narrate",
				Stage: "narrate",
				// review is a needs edge for its subsystems/security.json fragment —
				// narrate renders the security receipts from it. (perform carries the
				// rest of .stagefreight/, publish its publish.json fragment.)
				Needs:    []string{"perform", "review", "publish"},
				Commands: []string{"stagefreight ci run narrate"},
				// FullClone: the changelog facts walk tag history.
				Source: model.SourceSpec{FullClone: true, Submodules: cfg.Git.Submodules},
				Policy: model.PolicySpec{AllowFailure: true, WhenAlways: true},
			},
		},
	}

	// Per-job runner routing: a per-phase label set overrides the Default (which applies
	// to every job). Keeping the whole pipeline on ONE runner is what makes the local,
	// per-runner cache StageFreight configures persist across phases.
	for i := range p.Jobs {
		p.Jobs[i].Routing = model.RoutingSpec{Labels: cfg.CI.Routing.For(p.Jobs[i].Name)}
	}
	return p, nil
}

// releaseTagPatterns collects the git.tags patterns for the workflow tag gate:
// intent tags (matching a declared release pattern) spawn pipelines, state
// tags (scribe bookkeeping — latest, latest-dev, dev-<sha>) spawn nothing.
// Exclude patterns (! prefix) are skipped — a forge workflow rule can't
// express the binary's exclude-first evaluation, so the emitted gate is
// allow-listed on the include patterns and the binary's release-policy gate
// remains the authority inside the pipeline.
func releaseTagPatterns(cfg *config.Config) []string {
	var out []string
	for _, ts := range cfg.Git.Tags {
		if p := strings.TrimSpace(ts.Pattern); p != "" && !strings.HasPrefix(p, "!") {
			out = append(out, p)
		}
	}
	return out
}

// releaseTagRules lowers the git.tags sources for the forge workflow tag
// gate: intent tags (matching a declared release source) spawn pipelines,
// state tags (scribe bookkeeping — latest, latest-dev, dev-<sha>) spawn
// nothing. Each source is one rule — include or `!` exclude — mirroring
// tagMatchesReleasePolicy's OR across sources. A pattern that isn't valid
// RE2 (the binary matches those as literal strings, which forge rules can't)
// returns nil: the gate degrades to allow-all rather than risk a rule set
// more restrictive than the binary's, which could suppress a real release.
func releaseTagRules(cfg *config.Config) []model.TagRule {
	var out []model.TagRule
	for _, ts := range cfg.Git.Tags {
		p := strings.TrimSpace(ts.Pattern)
		if p == "" {
			continue
		}
		rule := model.TagRule{Pattern: p}
		if strings.HasPrefix(p, "!") {
			rule = model.TagRule{Pattern: p[1:], Exclude: true}
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return nil
		}
		out = append(out, rule)
	}
	return out
}

// packageRegistries lists the registries the publish job may push to, by config
// provider + credential env prefix, so each forge emitter can auto-wire the one(s)
// native to it (github→ghcr, gitea→gitea, …) with the forge's auto-token. Only
// registries declaring a credentials prefix qualify — the prefix names the env vars
// (<PREFIX>_USER / <PREFIX>_TOKEN) the forge supplies values for.
func packageRegistries(cfg *config.Config) []model.PackageRegistry {
	var out []model.PackageRegistry
	for _, r := range cfg.Registries {
		if r.Credentials == "" {
			continue
		}
		out = append(out, model.PackageRegistry{Provider: r.Provider, CredPrefix: r.Credentials})
	}
	return out
}
