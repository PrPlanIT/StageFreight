package cistate

// The fact VOCABULARY registry — the canonical list of every templateable fact
// name, kept adjacent to Fact() so the two evolve together. The docs freshness
// test walks this registry and fails when a name here is missing from the
// Narration & Notifications reference — a new fact ships WITH its documentation
// or not at all. When adding a fact: extend Fact() (bare names) or a subsystem
// recorder (domain names), add it here, document it.

// BareFacts are the always-resolvable identity and status facts served directly
// by Fact()'s switch — usable as {name} in any composed body.
var BareFacts = []string{
	"status", "status_icon", "status_verb",
	"project", "modality", "ref", "pipeline_url", "commit_title",
	"duration", "sha", "version",
}

// DomainFacts maps each fact domain to the metric keys its subsystem records —
// usable as {domain.key}. Every domain ALSO serves {domain.outcome} and
// {domain.reason} (the universal subsystem keys), and every metric elides when
// its domain recorded nothing. The special domains failure and retention are
// served by Fact() itself rather than a subsystem's Results.
var DomainFacts = map[string][]string{
	"failure":   {"subsystem", "reason"},
	"retention": {"pruned"},
	"publish":   {"tags", "registries"},
	"tests":     {"total", "passed", "coverage"},
	"security":  {"blocking", "critical", "high", "medium", "low", "total", "sbom", "blocking_list"},
	"changelog": {"count", "range"},
	"reconcile": {"total", "succeeded", "failed", "backend", "units", "cluster", "declined", "failures"},
	"ansible":   {"total", "converged", "changed", "unreachable", "failed"},
}
