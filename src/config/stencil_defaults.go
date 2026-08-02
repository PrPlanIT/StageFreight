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

// shippedSummary is the SUCCESS arc: what shipped/converged, the receipts, what
// changed. It is the UNION body — every modality's lines in one template,
// composed per-LINE: each line is single-domain and elides when its domain
// recorded nothing, so an image run, a gitops run, or a repo doing both all
// render coherently from this one body. Adding a modality = designing its fact
// vocabulary and appending its lines here — never a new template.
const shippedSummary = `{commit_title}
{sha} · {version}

Shipped {publish.tags} → {publish.registries}
{artifacts}
Converged {reconcile.succeeded}/{reconcile.total} {reconcile.units} on {reconcile.cluster}
Skipped {reconcile.declined} that failed validation

Tests — {tests.passed}/{tests.total} passed · {tests.coverage} coverage
Security — {security.blocking} blocking, {security.low} low CVEs · SBOM {security.sbom}

Commits since {changelog.range} — {changelog.count}
{changelog}

Pruned {retention.pruned} stale cache entries

→ {pipeline_url}
`

// shippedPostmortem is the FAILURE arc (union body, same per-line rule): leads
// with the break, receipts after, changelog reframed as waiting. Nothing shipped
// is said by the absent Shipped line.
const shippedPostmortem = `{commit_title}
{sha} · {version}

Failed in {failure.subsystem} — {failure.reason}
{failures}
{vulns}

Tests — {tests.passed}/{tests.total} passed · {tests.coverage} coverage
Converged {reconcile.succeeded}/{reconcile.total} {reconcile.units} on {reconcile.cluster}

Commits waiting since {changelog.range} — {changelog.count}

→ {pipeline_url}
`

// ShippedNotificationSubject is the default notification title — a freeform body
// like everything else. No {status_icon}: ntfy renders the notification's Tags
// emoji in front of the title already (white_check_mark / rotating_light), and
// two icons read as a stutter. The fact stays available for custom subjects.
// {duration} is elapsed from the run's first recorded state write.
const ShippedNotificationSubject = "{project} {ref} — {status} in {duration}"

// ShippedStencil returns SF's built-in stencil definition for id under the given
// lifecycle modality, if one exists. Callers must check user stencils FIRST —
// shadowing a shipped id is the override mechanism (redefine `changelog` with
// knobs — {type: ci, limit: 5} — and every {changelog} embed picks it up).
func ShippedStencil(id, modality string) (StencilDef, bool) {
	switch id {
	case "summary":
		// UNION bodies — one template for every modality (and any mix), composed
		// per-line; the modality param is deliberately unused.
		_ = modality
		return StencilDef{ID: id, Type: "text", Body: shippedSummary}, true
	case "postmortem":
		return StencilDef{ID: id, Type: "text", Body: shippedPostmortem}, true
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
