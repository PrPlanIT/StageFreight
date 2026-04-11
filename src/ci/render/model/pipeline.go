// Package model defines the forge-neutral CI pipeline types.
//
// These types are the shared contract between the planner (which builds
// a Pipeline from config) and forge emitters (which lower it to native YAML).
// This package has no dependencies on render or any forge emitter — it is
// the leaf of the import graph.
//
// Canonical rule: one pipeline graph for all modes. The planner never branches
// the graph by lifecycle mode. Mode dispatch is the binary's job at runtime.
package model

// Pipeline is the forge-neutral CI pipeline model.
// It describes what must execute, in what order, with what routing requirements.
// Forge emitters lower this to provider-native YAML.
type Pipeline struct {
	// Defaults are pipeline-level settings that apply to all jobs unless overridden.
	Defaults PipelineDefaults

	// Jobs is an ordered list of pipeline jobs. Ordering determines emit order.
	// The graph is structurally stable across all lifecycle modes.
	Jobs []Job
}

// PipelineDefaults are pipeline-level settings shared across all jobs.
// Emitters lower these to forge-native top-level blocks.
type PipelineDefaults struct {
	// Image is the default container image for all jobs.
	// GitLab: default.image. GitHub: container.image.
	Image string

	// Interruptible means jobs can be cancelled when a newer pipeline starts.
	// GitLab: default.interruptible. GitHub: concurrency.cancel-in-progress.
	Interruptible bool

	// CancelSuperseded means the forge should cancel in-flight pipelines
	// when a new commit arrives on the same ref.
	// GitLab: workflow.auto_cancel.on_new_commit.
	// GitHub: concurrency group with cancel-in-progress.
	CancelSuperseded bool

	// CIContext indicates this pipeline requires StageFreight CI context
	// hydration. Each emitter injects the appropriate mechanism:
	//   GitLab: before_script exporting SF_CI_* from CI_* variables
	//   GitHub: env block or setup step mapping GITHUB_* to SF_CI_*
	CIContext bool
}

// Job is a single pipeline job in the forge-neutral model.
type Job struct {
	// Name is the canonical phase name (audition, perform, review, publish, narrate).
	Name string

	// Stage is the stage this job belongs to. For most forges, stages determine
	// execution order at the scheduler level.
	Stage string

	// Needs lists jobs that must complete before this job runs.
	// Empty means no dependencies (first stage).
	Needs []string

	// Commands are the shell commands to execute in order.
	Commands []string

	// Source controls git clone behavior for this job.
	Source SourceSpec

	// Artifacts describes what this job produces and how long to keep it.
	Artifacts ArtifactSpec

	// Routing declares runner placement requirements for this job.
	// The forge emitter lowers Labels to forge-native routing primitives.
	Routing RoutingSpec

	// Capabilities declares what execution substrate this job requires.
	// The forge emitter uses these to inject services, variables, etc.
	Capabilities CapabilitySpec

	// Policy controls job-level scheduling and failure behavior.
	Policy PolicySpec
}

// SourceSpec controls git clone/fetch behavior for a job.
type SourceSpec struct {
	// FullClone requests an unshallow clone (git depth 0).
	// False means the forge's default shallow clone behavior.
	FullClone bool
}

// ArtifactSpec describes what a job produces.
type ArtifactSpec struct {
	// Paths lists artifact paths to collect after the job completes.
	Paths []string

	// ExpireIn is a human-readable duration string (e.g. "1 week", "2 hours").
	// Empty means the forge default.
	ExpireIn string
}

// RoutingSpec declares runner placement requirements for a job.
// Labels are forge-agnostic; each emitter lowers them to native primitives.
type RoutingSpec struct {
	// Labels are runner selection labels. Empty means no routing constraint.
	//   GitLab emitter:             tags: [label...]
	//   GitHub/Gitea/Forgejo:       runs-on: [label...]
	Labels []string
}

// CapabilitySpec declares what execution substrate a job requires.
// Emitters use these to inject the appropriate services and variables.
type CapabilitySpec struct {
	// Docker indicates this job requires Docker daemon access (DinD).
	Docker bool

	// OIDC indicates this job requires an OIDC identity token.
	// GitLab emitter adds id_tokens.STAGEFREIGHT_OIDC.
	OIDC bool
}

// PolicySpec controls job-level scheduling and failure behavior.
type PolicySpec struct {
	// AllowFailure means the pipeline continues even if this job fails.
	AllowFailure bool

	// WhenAlways means this job runs regardless of prior job outcomes.
	// Used for narrate/docs jobs that must always emit truth.
	WhenAlways bool
}
