package commit

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// StageMode determines how files are staged before commit.
type StageMode string

const (
	StageExplicit StageMode = "explicit" // --add paths
	StageAll      StageMode = "all"      // --all (git add -A)
	StageStaged   StageMode = "staged"   // default: commit whatever is already staged
)

// PushOptions controls post-commit push behavior.
type PushOptions struct {
	Enabled         bool
	Remote          string // default: "origin"
	Refspec         string // default: "" (current branch)
	RebaseOnDiverge bool   // when true (default), rebase onto upstream if diverged before pushing
}

// Plan is a fully resolved, validated commit intent.
type Plan struct {
	Type      string
	Scope     string
	Summary   string
	Body     string
	Breaking bool
	// SkipCI is the manual `stagefreight commit --skip-ci` escape hatch — it appends the
	// forge-owned [skip ci] subject token (the opt-in fallback). Automated paths do NOT use
	// it; they set Origin instead, so their loop-prevention never poisons a tag pipeline.
	SkipCI bool
	// Origin names the StageFreight path that authored this commit (config.OriginNarrate /
	// OriginDeps), which selects its provenance trailer (Generated-By / Updated-By). Empty
	// for a manual `stagefreight commit` or human commit — no trailer, builds by default.
	Origin    string
	Paths     []string // for StageExplicit
	StageMode StageMode
	Push      PushOptions
	SignOff   bool
}

// Subject renders the commit subject line.
// When conventional is true: {type}[({scope})][!]: {summary}
// When conventional is false: {summary}
//
// A CI-skip request is NOT written into the subject as a [skip ci] token: that token is
// forge-owned and context-blind, so it also suppresses tag/release pipelines and is
// invisible to local execution. Loop-prevention is signalled by a trailer instead (see
// Message) that StageFreight's own rendered CI rules honor, scoped to branch pushes.
func (p Plan) Subject(conventional bool) string {
	var subject string
	if conventional {
		subject = p.Type
		if p.Scope != "" {
			subject += fmt.Sprintf("(%s)", p.Scope)
		}
		if p.Breaking {
			subject += "!"
		}
		subject += ": " + p.Summary
	} else {
		subject = p.Summary
	}
	// Manual --skip-ci opt-in: the forge-owned [skip ci] token. Automated paths never take
	// this route (they set Origin → a provenance trailer), so no generated commit poisons a
	// tag pipeline via a subject token.
	if p.SkipCI {
		subject += " [skip ci]"
	}
	return subject
}

// Message renders the full commit message (subject + optional body + trailers).
// The SF-generated trailer is always appended so the replay gate can identify and safely
// rebase these commits. When SkipCI is set (a generated-artifact refresh — docs/narrate/
// deps), the CI-skip trailer is added too: StageFreight's rendered trigger rules skip a
// NEW pipeline for such a commit on a branch, while tags always build — replacing the
// blunt [skip ci] subject token with a signal we own and interpret across forge + local.
func (p Plan) Message(conventional bool) string {
	msg := p.Subject(conventional)
	if p.Body != "" {
		msg += "\n\n" + p.Body
	}
	if t := config.OriginTrailer(p.Origin); t != "" {
		msg += "\n\n" + t
	}
	return msg
}
