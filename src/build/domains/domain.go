// Package domains makes a perform run a single domain-ordered narrative.
//
// The execution story of a run is owned by DOMAINS (Detect, Plan, Build, Verify,
// Publish), not by build pipelines. Build strategies — binary, docker/crucible,
// a future rpm — are CONTRIBUTORS: capabilities that supply rows into the domain
// that owns the work. A contributor never owns a domain spine; adding a new
// strategy adds rows under Build/Publish, never a new Detect/Plan/Build/Summary.
//
// The full top-level narrative of a run is exactly:
//
//	Identity → Executor → Lint → Detect → Plan → Build → Verify → Publish → Summary
//
// Everything else (Crucible Context, Builder, Cache, Content Store, Cache
// Retention, Provenance, …) is a SUBSECTION of one of those domains, never a
// top-level peer. Identity/Executor/Lint/Summary are run-level — rendered once by
// the run. The Run is the single owner of run-level state: one Summary, one
// outputs.json/published.json pair.
package domains

import (
	"context"
	"io"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// Domain is a user-visible execution stage. Domains own execution order and
// presentation; contributors supply contributions.
type Domain string

const (
	DomainDetect  Domain = "detect"
	DomainPlan    Domain = "plan"
	DomainBuild   Domain = "build"
	DomainVerify  Domain = "verify"
	DomainPublish Domain = "publish"
)

// orderedDomains is the canonical contributor-domain order the runner iterates.
var orderedDomains = []Domain{DomainDetect, DomainPlan, DomainBuild, DomainVerify, DomainPublish}

// title is the rendered heading for a domain box.
func (d Domain) title() string {
	switch d {
	case DomainDetect:
		return "Detect"
	case DomainPlan:
		return "Plan"
	case DomainBuild:
		return "Build"
	case DomainVerify:
		return "Verify"
	case DomainPublish:
		return "Publish"
	default:
		return string(d)
	}
}

// RunContext is the single owner of run-level truth. There is one per perform
// run. Every contributor records produced artifacts into the SAME Outputs/RB,
// and the run writes the one manifest pair once at the end (fixing the prior
// clobber where two pipelines each wrote outputs.json/published.json).
type RunContext struct {
	Ctx      context.Context
	RootDir  string
	Config   *config.Config
	Writer   io.Writer
	Stderr   io.Writer
	Color    bool
	Verbose  bool
	SkipLint bool
	DryRun   bool // stop after Plan: render Detect+Plan, skip Build/Verify/Publish + manifests
	Store    cas.Store
	Target   string // optional build target (docker), ignored by strategies that don't use it

	// Generic build-selection options understood by every strategy. Set by the
	// command entrypoint; contributors read them in Plan.
	Local     bool     // build only the current platform
	Platforms []string // override platforms (e.g. "linux/arm64")
	BuildID   string   // restrict to a single build entry by ID

	// Only restricts the run to the named contributors (e.g. ["binary"] for the
	// standalone `build binary` command). Empty means every applicable
	// contributor runs (perform).
	Only []string

	// Outputs + RB are the single shared manifest pair for the whole run.
	// Contributors append artifacts to Outputs and record outcomes into RB; the
	// run finalizes + writes both exactly once after every domain has run.
	Outputs *artifact.OutputsManifest
	RB      *build.ResultsBuilder
}

// Contribution is what a contributor returns for one domain call: pre-formatted
// content rows to render under the domain box (the renderer owns the box
// framing; the contributor owns its row content), plus a one-line summary and
// status for the single run Summary.
type Contribution struct {
	Rows    []string // content lines — no box framing; the renderer frames each
	Status  string   // "success" | "failed" | "skipped" — folded into the Summary
	Summary string   // one-line detail for the run Summary
	Skip    bool     // contributor participates in this domain but had no work this run
}

// Contributor is a build capability (binary, docker/crucible, archive, …). It
// joins a domain by implementing that domain's interface below. It NEVER owns a
// domain spine — it only supplies contributions.
type Contributor interface {
	Name() string
	Order() int                  // lower renders first within a domain (binary=10, docker=20)
	Applies(rc *RunContext) bool // does this run have work for this contributor?
}

// Per-domain interfaces. A contributor implements only the domains it joins;
// the runner type-asserts to discover participation.
type (
	Detector  interface{ Detect(rc *RunContext) (Contribution, error) }
	Planner   interface{ Plan(rc *RunContext) (Contribution, error) }
	Builder   interface{ Build(rc *RunContext) (Contribution, error) }
	Verifier  interface{ Verify(rc *RunContext) (Contribution, error) }
	Publisher interface{ Publish(rc *RunContext) (Contribution, error) }
)

// SubstrateNeeds lets a contributor declare what the run's single Executor check
// must verify. The run ORs the needs of the ACTIVE contributors — so standalone
// `build binary` (binary only) does not demand docker, while perform (crucible
// active) does. Contributors needing nothing special omit this interface.
type SubstrateNeeds interface {
	NeedsDocker() bool
	NeedsCrucible() bool
}

// Concluder lets a contributor render a closing flourish AFTER the run Summary
// (e.g. crucible's Verdict). The run calls it on every active contributor that
// implements it, once, after the Summary — on both the success and the
// build-failure paths — so the Verdict reads as the run's final word rather than
// appearing mid-Publish.
type Concluder interface {
	Conclude(rc *RunContext)
}
