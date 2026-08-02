package config

// Shipped default stencils — SF's built-in audience text, expressed in the SAME
// grammar an operator writes (type: text bodies of facts + embeds; every label,
// verb, and separator is body text). Resolution order puts these LAST: a user
// stencil with the same id shadows the shipped one — that IS the override
// mechanism, and overriding the body is also how a team ships another language.
//
// Bodies are modality-resolved behind stable ids: {summary} means "this run's
// summary" everywhere; which body backs it depends on the run's modality. gitops/
// governance currently inherit the image bodies until their fact vocabularies are
// designed against real runs (same seam narrate's old per-modality template had).
//
// Copy rules these bodies follow (the product-copy bar): visually scannable, no
// insider vocabulary, labels ARE the information for counts, one domain per line
// (so line elision drops whole lines with their labels when a domain recorded
// nothing), and the pipeline link stays last for tap-through.

// shippedSummaryImage is the SUCCESS arc: what shipped, the receipts, what changed.
const shippedSummaryImage = `{commit_title}
{sha} · {version}

Shipped {publish.tags} → {publish.registries}
{artifacts}

Tests — {tests.passed}/{tests.total} passed · {tests.coverage} coverage
Security — {security.blocking} blocking, {security.low} low CVEs · SBOM {security.sbom}

Commits since {changelog.range} — {changelog.count}
{changelog}

Pruned {retention.pruned} stale cache entries

→ {pipeline_url}
`

// shippedPostmortemImage is the FAILURE arc: leads with the break, receipts after,
// changelog reframed as waiting. Nothing shipped is said by the absent Shipped line.
const shippedPostmortemImage = `{commit_title}
{sha} · {version}

Failed in {failure.subsystem} — {failure.reason}
{failures}
{vulns}

Tests — {tests.passed}/{tests.total} passed · {tests.coverage} coverage

Commits waiting since {changelog.range} — {changelog.count}

→ {pipeline_url}
`

// ShippedNotificationSubject is the default notification title — a freeform body
// like everything else ({duration} joins when a duration source lands). No
// {status_icon}: ntfy renders the notification's Tags emoji in front of the
// title already (white_check_mark / rotating_light), and two icons read as a
// stutter. The fact stays available for custom subjects.
const ShippedNotificationSubject = "{project} {ref} — {status}"

// ShippedStencil returns SF's built-in stencil definition for id under the given
// lifecycle modality, if one exists. Callers must check user stencils FIRST —
// shadowing a shipped id is the override mechanism (redefine `changelog` with
// knobs — {type: ci, limit: 5} — and every {changelog} embed picks it up).
func ShippedStencil(id, modality string) (StencilDef, bool) {
	switch id {
	case "summary":
		// Modality seam: image/docker (and, for now, everything else) share the
		// image bodies; gitops/governance bodies land with their fact vocabularies.
		_ = modality
		return StencilDef{ID: id, Type: "text", Body: shippedSummaryImage}, true
	case "postmortem":
		return StencilDef{ID: id, Type: "text", Body: shippedPostmortemImage}, true
	// The four looping producers — the only run data that earns presentation
	// config. Self-bounding rows read from cistate; empty renders nothing.
	case "failures", "vulns", "artifacts", "changelog":
		return StencilDef{ID: id, Type: "ci", Section: id}, true
	}
	return StencilDef{}, false
}

// IsShippedStencilID reports whether id names a shipped default stencil (valid to
// announce or embed without a user declaration).
func IsShippedStencilID(id string) bool {
	_, ok := ShippedStencil(id, "")
	return ok
}
