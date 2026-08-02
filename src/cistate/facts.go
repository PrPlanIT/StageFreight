package cistate

import (
	"fmt"
	"strconv"
	"strings"
)

// Fact resolves a stencil fact token against the run's recorded truth. Facts are
// the deterministic data half of the stencil language (the author owns the framing):
//
//   - Bare status facts — {status}, {status_icon}, {status_verb} — derive from
//     PipelineStatus and are status-CARRYING: they resolve differently on pass/fail,
//     which is how bodies alternate without conditionals.
//   - Dotted facts — {<domain>.<key>} — read a subsystem's recorded Results
//     ({tests.passed}, {security.blocking}, {publish.tags}), plus the lifecycle
//     fields every subsystem carries ({security.outcome}, {security.reason}).
//     {failure.subsystem}/{failure.reason} derive from the first hard-failed
//     subsystem; {retention.pruned} from the retention records.
//
// Resolution semantics honor the naming split: a DOTTED token is fact namespace by
// construction, so it always resolves (ok=true) — to "" when the domain recorded
// nothing, which is what makes presence-elision work ("no data → the line vanishes").
// A BARE token that isn't a status fact returns ok=false so unknown bare tokens stay
// visibly literal (typo, or a token for the downstream gitver leaf-pass).
func (st *State) Fact(name string) (string, bool) {
	if st == nil {
		return "", false
	}
	switch name {
	case "status":
		return statusWord(st.PipelineStatus()), true
	case "status_icon":
		return statusIcon(st.PipelineStatus()), true
	case "status_verb":
		return statusVerb(st.PipelineStatus()), true
	// Identity facts — always-known names; empty when the run didn't record them.
	case "project":
		return st.CI.Project, true
	case "modality":
		return st.CI.Modality, true
	case "ref":
		return st.CI.Ref, true
	case "pipeline_url":
		return st.CI.PipelineURL, true
	case "commit_title":
		return st.CI.CommitTitle, true
	case "duration":
		return humaneDuration(st.CI.DurationSecs), true
	// {sha}/{version} are gitver's keywords; the RUN's recorded identity wins when
	// present (ok only when non-empty, so a local render without state falls
	// through to the gitver leaf-pass instead of resolving to nothing).
	case "sha":
		if st.CI.SHA == "" {
			return "", false // no run identity → the token stays gitver's
		}
		return shortSHA(st.CI.SHA), true
	case "version":
		if st.CI.SHA == "" {
			return "", false // no run identity → the token stays gitver's
		}
		// Run identity present: an unversioned repo (gitops) records no version —
		// the token elides rather than rendering a literal {version} on the phone.
		return st.CI.Version, true
	}

	domain, key, dotted := strings.Cut(name, ".")
	if !dotted {
		return "", false
	}

	switch domain {
	case "failure":
		sub := st.firstFailure()
		if sub == nil {
			return "", true
		}
		switch key {
		case "subsystem":
			return sub.Name, true
		case "reason":
			return sub.Reason, true
		}
		return "", true

	case "retention":
		if key == "pruned" {
			total := 0
			if r := st.Retention.Local; r != nil {
				total += r.Pruned
			}
			if r := st.Retention.External; r != nil {
				total += r.Pruned
			}
			if total == 0 {
				return "", true // nothing pruned → the line elides
			}
			return strconv.Itoa(total), true
		}
		return "", true
	}

	sub := st.GetSubsystem(factDomainSubsystem(domain))
	if sub == nil {
		return "", true // domain recorded nothing → every metric elides
	}
	switch key {
	case "outcome":
		return sub.Outcome, true
	case "reason":
		return sub.Reason, true
	}
	return sub.Results[key], true
}

// humaneDuration renders elapsed seconds the way a human says them ("42s",
// "3m 12s", "1h 04m"). Zero/negative → "" (unrecorded → the token elides).
func humaneDuration(secs int) string {
	if secs <= 0 {
		return ""
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
	}
	return fmt.Sprintf("%dh %02dm", secs/3600, (secs%3600)/60)
}

// shortSHA abbreviates a commit SHA to the 7-hex display form.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// factDomainSubsystem maps a fact domain to its subsystem name. The fact vocabulary
// says {tests.*} (a dev's word); the subsystem registers as "test".
func factDomainSubsystem(domain string) string {
	if domain == "tests" {
		return "test"
	}
	return domain
}

// firstFailure returns the first attempted subsystem whose failure fails the
// pipeline (outcome failed, not allow-failure), or — when none is hard-failing —
// the first attempted failed subsystem of any kind. Nil when nothing failed.
func (st *State) firstFailure() *SubsystemState {
	var soft *SubsystemState
	for i := range st.Subsystems {
		s := &st.Subsystems[i]
		if !s.Attempted || s.Outcome != "failed" {
			continue
		}
		if !s.AllowFailure {
			return s
		}
		if soft == nil {
			soft = s
		}
	}
	return soft
}

// statusWord renders the aggregate status as the sentence-level word the subject
// line uses ("{project} — {status} in {duration}").
func statusWord(pipeline string) string {
	switch pipeline {
	case "passing":
		return "passed"
	case "failing":
		return "failed"
	case "warning":
		return "passed with warnings"
	default:
		return "unknown"
	}
}

// statusIcon is the status-carrying glyph fact.
func statusIcon(pipeline string) string {
	switch pipeline {
	case "passing":
		return "✅"
	case "failing":
		return "🚨"
	case "warning":
		return "⚠️"
	default:
		return "❔"
	}
}

// statusVerb is the sentence-leading form ("Passed", "Failed", …).
func statusVerb(pipeline string) string {
	switch pipeline {
	case "passing":
		return "Passed"
	case "failing":
		return "Failed"
	case "warning":
		return "Passed with warnings"
	default:
		return "Ran"
	}
}
