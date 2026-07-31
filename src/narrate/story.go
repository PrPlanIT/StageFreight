package narrate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/stencil"
)

// Pipeline status values (mirror cistate.State.PipelineStatus()).
const (
	StatusPassing = "passing"
	StatusWarning = "warning"
	StatusFailing = "failing"
	StatusUnknown = "unknown"
)

// Input is the run's structured truth, flattened to plain data. narrate is the
// first native consumer of the stencil engine: it turns Input into a stencil.Env
// (facts + composed elements) and stamps the run's story stencil. No cistate/git/
// disk IO happens here — that's what makes the render pure and the determinism
// test trivial; the runner (cli/cmd) gathers Input from cistate + the changelog.
//
// Facts vs framing (the stencil design line): the resolver below yields SF-owned
// FACTS ({ci.*}, {changelog}, {image.*}); the story stencil is the FRAMING the
// author controls. Skeleton scope — fields map to what cistate carries today; fine
// artifact detail (digests, per-tag/registry lists, asset counts) is a later
// manifest enrichment, absent here and degraded gracefully, never faked.
type Input struct {
	Project     string // {project.name}
	Description string // {project.description}
	Modality    string // {ci.modality}: image | gitops | governance | docker
	Status      string // passing | warning | failing | unknown
	Ref         string // {ci.ref}
	SHA         string // {ci.sha} (short)
	Version     string // {version} (optional)
	PipelineURL string // {ci.pipeline_url} (optional)

	Phases      []Phase       // ordered lifecycle subsystems (perform/review/publish/…)
	Changes     []ChangeGroup // the release changelog, grouped (Features/Fixes/…)
	ChangeCount int           // total commits in the changelog range
	SinceRef    string        // previous release tag the range starts after (optional)

	Published  int      // {image.count}: images published this run
	Registries []string // {image.registries}
	ReleaseTag string   // {ci.release.tag} (optional)
	ReleaseURL string   // release page URL (optional)

	Housekeeping []string // retention/mirror one-liners (optional)

	// Overrides lets a user stencil (cfg.Stencils) override any built-in element by id —
	// framing is the author's, facts stay SF's. Populated by the runner (thin hook).
	Overrides map[string]string

	Template string // stencil override; empty → embedded modality default
}

// Phase is one lifecycle subsystem's outcome (a cistate.SubsystemState view).
type Phase struct {
	Name     string
	Outcome  string // success | failed | skipped | warning | not_applicable | cancelled
	Reason   string
	Blocking bool
}

// ChangeGroup is one changelog category (from release.Categorize).
type ChangeGroup struct {
	Title   string // "Features", "Bug Fixes", …
	Entries []ChangeEntry
}

// ChangeEntry is a single changelog line.
type ChangeEntry struct {
	Scope    string
	Summary  string
	Breaking bool
}

// RenderStory stamps the run's story stencil: it wires Input into a stencil.Env and
// renders the modality's default stencil (or the caller's override). Pure and
// deterministic — same Input, same Markdown.
func RenderStory(in Input) string {
	tmpl := in.Template
	if tmpl == "" {
		tmpl = defaultTemplate(in.Modality)
	}
	return stencil.Render(tmpl, in.env())
}

// env exposes Input to the stencil engine: facts and composed elements resolve
// through one switch (a scalar fact and a composed element read identically —
// {ci.sha} and {shipped} are the same kind of embed), conditionals through another.
func (in Input) env() stencil.Env {
	return stencil.Env{Resolve: in.resolve, Cond: in.cond}
}

func (in Input) resolve(name string) (string, bool) {
	// A user stencil (cfg.Stencils) may override any built-in element by id — the
	// author owns the framing; SF owns the facts underneath. (Thin hook: the runner
	// populates Overrides; deeper config→narrate wiring is deferred.)
	if v, ok := in.Overrides[name]; ok {
		return v, true
	}
	switch name {
	// ── facts (SF-owned, structured) ──
	case "project.name":
		return in.Project, true
	case "project.description":
		return in.Description, true
	case "ci.modality":
		return in.Modality, true
	case "ci.status":
		return humanStatus(in.Status), true
	case "ci.ref":
		return in.Ref, true
	case "ci.sha":
		return in.SHA, true
	case "ci.pipeline_url":
		return in.PipelineURL, true
	case "ci.release.tag":
		return in.ReleaseTag, true
	case "ci.failure.subsystem":
		if p := in.firstBlocking(); p != nil {
			return p.Name, true
		}
		return "", true
	case "ci.failure.reason":
		if p := in.firstBlocking(); p != nil {
			return p.Reason, true
		}
		return "", true
	case "version":
		return in.Version, true
	case "changelog":
		return in.changelog(), true
	case "changelog.range":
		return in.changeRange(), true
	case "image.count":
		return strconv.Itoa(in.Published), true
	case "image.registries":
		return strings.Join(in.Registries, " · "), true
	case "ci.status_verb":
		return statusVerb(in.Status), true
	case "ci.ship_apex":
		return in.shippedApex(), true
	// ── composed elements (framing helpers; still just elements) ──
	case "shipped":
		return in.shipped(), true
	case "acts":
		return in.acts(), true
	case "failures":
		return in.failures(), true
	case "coda":
		return in.coda(), true
	}
	return "", false
}

func (in Input) cond(name string) bool {
	switch name {
	case "failed":
		return in.Status == StatusFailing
	case "has_version":
		return in.Version != ""
	case "has_pipeline":
		return in.PipelineURL != ""
	case "has_housekeeping":
		return len(in.Housekeeping) > 0
	}
	return false
}

// shippedApex is the {ci.ship_apex} fact — the trailing " — shipped …" clause the
// story stencil appends to a passing verdict — or "" when nothing distributable was
// produced. The verdict itself is now framing: it lives in the modality stencil as
// {#if failed}…{#else}{ci.status_verb}{ci.ship_apex}.{/if}, not in Go.
func (in Input) shippedApex() string {
	switch {
	case in.ReleaseTag != "" && in.Published > 0:
		return fmt.Sprintf(" — shipped `%s` to %s", in.ReleaseTag, plural(in.Published, "image", "images"))
	case in.ReleaseTag != "":
		return fmt.Sprintf(" — cut `%s`", in.ReleaseTag)
	case in.Published > 0:
		return fmt.Sprintf(" — shipped %s", plural(in.Published, "image", "images"))
	default:
		return ""
	}
}

// changeRange is the "(N commits since <ref>)" suffix on the Changes heading.
func (in Input) changeRange() string {
	if in.ChangeCount == 0 {
		return ""
	}
	n := plural(in.ChangeCount, "commit", "commits")
	if in.SinceRef != "" {
		return fmt.Sprintf("_(%s since `%s`)_", n, in.SinceRef)
	}
	return fmt.Sprintf("_(%s)_", n)
}

// changelog renders the grouped changelog — the substance of the run. Empty range
// degrades to a single honest line.
func (in Input) changelog() string {
	if len(in.Changes) == 0 {
		return "_No code changes since the last release._"
	}
	var b strings.Builder
	for i, g := range in.Changes {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "**%s**\n", g.Title)
		for _, e := range g.Entries {
			b.WriteString("- ")
			if e.Breaking {
				b.WriteString("**BREAKING** ")
			}
			if e.Scope != "" {
				fmt.Fprintf(&b, "**%s:** ", e.Scope)
			}
			b.WriteString(e.Summary)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// failures lists the failing subsystems with reasons — the post-mortem branch.
func (in Input) failures() string {
	var lines []string
	for _, p := range in.Phases {
		if p.Outcome != "failed" {
			continue
		}
		if p.Reason != "" {
			lines = append(lines, fmt.Sprintf("- **%s** — %s", p.Name, p.Reason))
		} else {
			lines = append(lines, fmt.Sprintf("- **%s** failed", p.Name))
		}
	}
	if len(lines) == 0 {
		return "_No subsystem reported a failure reason._"
	}
	return strings.Join(lines, "\n")
}

// acts is the phase run-line: "perform ✓ · review ✓ · publish ✗".
func (in Input) acts() string {
	var parts []string
	for _, p := range in.Phases {
		parts = append(parts, fmt.Sprintf("%s %s", p.Name, outcomeIcon(p.Outcome)))
	}
	if len(parts) == 0 {
		return "_No phases recorded._"
	}
	return strings.Join(parts, " · ")
}

// shipped lists the distributable artifacts. Skeleton fidelity: counts + release
// tag/link (digests and asset breakdowns arrive with the manifest enrichment).
func (in Input) shipped() string {
	var lines []string
	if in.Published > 0 {
		line := fmt.Sprintf("- **images** — %s published", plural(in.Published, "image", "images"))
		if len(in.Registries) > 0 {
			line += " across " + strings.Join(in.Registries, " · ")
		}
		lines = append(lines, line)
	}
	if in.ReleaseTag != "" {
		if in.ReleaseURL != "" {
			lines = append(lines, fmt.Sprintf("- **release** — [`%s`](%s)", in.ReleaseTag, in.ReleaseURL))
		} else {
			lines = append(lines, fmt.Sprintf("- **release** — `%s`", in.ReleaseTag))
		}
	}
	if len(lines) == 0 {
		return "_Nothing distributable shipped this run._"
	}
	return strings.Join(lines, "\n")
}

// coda is the housekeeping footer: retention pruned, mirror synced.
func (in Input) coda() string {
	if len(in.Housekeeping) == 0 {
		return ""
	}
	var lines []string
	for _, h := range in.Housekeeping {
		lines = append(lines, "- "+h)
	}
	return strings.Join(lines, "\n")
}

func (in Input) firstBlocking() *Phase {
	for i := range in.Phases {
		if in.Phases[i].Outcome == "failed" {
			return &in.Phases[i]
		}
	}
	return nil
}

func humanStatus(status string) string {
	switch status {
	case StatusPassing:
		return "passed"
	case StatusWarning:
		return "passed with warnings"
	case StatusFailing:
		return "failed"
	default:
		return "ran"
	}
}

// statusVerb is the {ci.status_verb} fact — the capitalized verb the story stencil
// leads the verdict with (Passed / Failed / Passed with warnings / Ran).
func statusVerb(status string) string {
	switch status {
	case StatusPassing:
		return "Passed"
	case StatusWarning:
		return "Passed with warnings"
	case StatusFailing:
		return "Failed"
	default:
		return "Ran"
	}
}

func outcomeIcon(outcome string) string {
	switch outcome {
	case "success":
		return "✓"
	case "failed":
		return "✗"
	case "warning":
		return "⚠"
	default: // skipped | not_applicable | cancelled | ""
		return "⊘"
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
