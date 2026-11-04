// Package workflow checks a local action against the repo's declared workflow: — the local
// boundary of workflow governance. It is advisory: the forge enforces the rules for real, so
// this makes the declared path the default and explains it, and an override is always
// possible. Absent a workflow: block, nothing here applies.
package workflow

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Finding is one rule an action breaks.
type Finding struct {
	Rule    string // stable id: landing, release
	Class   string // the branch class whose rule it is; empty when no class applies
	Message string
	Teach   string // what to do instead
	// Evaluate is set when the rule is declared enforcement: evaluate: reported, never
	// refused.
	Evaluate bool
}

// ClassOf returns the declared class whose git.branches matcher selects branch. Classes
// are tried in declaration order.
func ClassOf(cfg *config.Config, branch string) (config.BranchClass, bool) {
	for _, c := range cfg.Workflow.Branches {
		re, err := regexp.Compile(cfg.Git.Branches[c.Match])
		if err != nil || cfg.Git.Branches[c.Match] == "" {
			continue
		}
		if re.MatchString(branch) {
			return c, true
		}
	}
	return config.BranchClass{}, false
}

// Landing checks a direct change onto branch, by commit or push (verb says which). A class
// that lands through merge requests refuses it.
func Landing(cfg *config.Config, branch, verb string) *Finding {
	if cfg == nil || !cfg.Workflow.Present() || branch == "" {
		return nil
	}
	c, ok := ClassOf(cfg, branch)
	if !ok || c.Enforcement == config.EnforcementOff || c.Landing != config.LandingMergeRequest {
		return nil
	}
	return &Finding{
		Rule:     "landing",
		Class:    c.ID,
		Message:  fmt.Sprintf("%s is a %s branch: changes land on it through a merge request, not a direct %s", branch, c.ID, verb),
		Teach:    landingTeach(cfg, c),
		Evaluate: c.Enforcement == config.EnforcementEvaluate,
	}
}

// landingTeach names the class a change starts on before it lands on c, from the declared
// merge-request promotion into c.
func landingTeach(cfg *config.Config, c config.BranchClass) string {
	for _, p := range cfg.Workflow.Promote {
		if p.To != c.ID || p.Method != config.PromoteMergeRequest {
			continue
		}
		for _, from := range cfg.Workflow.Branches {
			if from.ID == p.From {
				return fmt.Sprintf("commit on a %s branch (matching %s) and open a merge request into %s", from.ID, cfg.Git.Branches[from.Match], c.ID)
			}
		}
	}
	return fmt.Sprintf("commit on another branch and open a merge request into %s", c.ID)
}

// Kind classifies a release tag by its version: prerelease, major, minor or patch. A tag
// that isn't a version has no kind.
func Kind(tag string) (string, bool) {
	v, err := semver.NewVersion(strings.TrimPrefix(tag, "v"))
	if err != nil {
		return "", false
	}
	switch {
	case v.Prerelease() != "":
		return "prerelease", true
	case v.Patch() > 0:
		return "patch", true
	case v.Minor() > 0:
		return "minor", true
	default:
		return "major", true
	}
}

// Release checks a release tag against workflow.release.from: the tagged commit must be on
// a branch of a class allowed to carry the tag's kind. branches are the branches that
// contain the commit.
func Release(cfg *config.Config, tag string, branches []string) *Finding {
	if cfg == nil || !cfg.Workflow.Present() || len(cfg.Workflow.Release.From) == 0 {
		return nil
	}
	kind, ok := Kind(tag)
	if !ok {
		return nil
	}
	var allowed []string
	for _, c := range cfg.Workflow.Branches {
		if slices.Contains(cfg.Workflow.Release.From[c.ID], kind) {
			allowed = append(allowed, c.ID)
		}
	}
	// The strictest enforcement among the classes the commit is on decides; a commit on no
	// governed class is refused outright.
	evaluate, governed := true, false
	for _, b := range branches {
		c, ok := ClassOf(cfg, b)
		if !ok || c.Enforcement == config.EnforcementOff {
			continue
		}
		if slices.Contains(allowed, c.ID) {
			return nil
		}
		governed = true
		if c.Enforcement != config.EnforcementEvaluate {
			evaluate = false
		}
	}
	allows := "allows no class to carry"
	if len(allowed) > 0 {
		allows = "allows only from " + strings.Join(allowed, ", ")
	}
	return &Finding{
		Rule:     "release",
		Message:  fmt.Sprintf("%s is a %s release, which workflow.release.from %s; the tagged commit is on %s", tag, kind, allows, onBranches(branches)),
		Teach:    "tag a commit on a branch of an allowed class, or declare the kind for this class in workflow.release.from",
		Evaluate: governed && evaluate,
	}
}

func onBranches(branches []string) string {
	if len(branches) == 0 {
		return "no branch"
	}
	return strings.Join(branches, ", ")
}

// Promotion resolves which declared promotion carries a change out of branch: the one
// named id, or, with no id, the only promotion out of the branch's class.
func Promotion(cfg *config.Config, branch, id string) (config.Promotion, error) {
	if cfg == nil || !cfg.Workflow.Present() {
		return config.Promotion{}, fmt.Errorf("no workflow: declared, so there's nothing to promote along")
	}
	if id != "" {
		for _, p := range cfg.Workflow.Promote {
			if p.ID == id {
				return p, nil
			}
		}
		return config.Promotion{}, fmt.Errorf("no promotion %q in workflow.promote", id)
	}
	c, ok := ClassOf(cfg, branch)
	if !ok {
		return config.Promotion{}, fmt.Errorf("%s belongs to no workflow.branches class, so no promotion starts from it", branch)
	}
	var out []config.Promotion
	for _, p := range cfg.Workflow.Promote {
		if p.From == c.ID {
			out = append(out, p)
		}
	}
	switch len(out) {
	case 0:
		return config.Promotion{}, fmt.Errorf("no promotion starts from the %s class", c.ID)
	case 1:
		return out[0], nil
	}
	ids := make([]string, len(out))
	for i, p := range out {
		ids[i] = p.ID
	}
	return config.Promotion{}, fmt.Errorf("several promotions start from the %s class; name one (%s)", c.ID, strings.Join(ids, ", "))
}
