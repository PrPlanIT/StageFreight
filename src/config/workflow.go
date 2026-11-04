package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowConfig is the workflow: block: how code travels into a repo. It declares the
// branch classes, how a change moves between them, and which classes may carry which
// release tags. Every branching model (trunk, GitHub flow, gitflow, a release train,
// environment branches) is a preset over these same fields; StageFreight enforces what
// is declared and imposes no strategy of its own. Presence-gated: absent, nothing about
// branches is governed.
type WorkflowConfig struct {
	Preset string `yaml:"preset,omitempty"`

	// Branches declares the branch classes as an id→class map. A class names the
	// git.branches matcher that selects its branches.
	Branches OrderedBranchClasses `yaml:"branches,omitempty"`

	// Promote declares how a change moves between classes as an id→promotion map.
	Promote OrderedPromotions `yaml:"promote,omitempty"`

	// Release declares where release tags may be cut.
	Release WorkflowRelease `yaml:"release,omitempty"`
}

// Present reports whether the repo declares a workflow.
func (w WorkflowConfig) Present() bool {
	return len(w.Branches) > 0 || len(w.Promote) > 0 || len(w.Release.From) > 0
}

// BranchClass is one class of branches and the rules that govern landing on them.
type BranchClass struct {
	ID string `yaml:"-"`

	// Match names the git.branches matcher that selects this class's branches.
	Match string `yaml:"match"`

	// Lifetime says how branches of this class come and go:
	//   long (default): permanent, such as a trunk.
	//   short:          a line of work that merges and is deleted; MaxAge bounds it.
	//   on_demand:      cut when needed, such as a release line cut from a tag.
	Lifetime string `yaml:"lifetime,omitempty"`

	// MaxAge is how long a short-lived branch may live before it is reported stale.
	MaxAge string `yaml:"max_age,omitempty"`

	// CreatedFrom says what an on-demand branch is cut from: another class's id, or tag.
	CreatedFrom string `yaml:"created_from,omitempty"`

	// Enforcement is active (default), evaluate (report what would be refused without
	// refusing it), or off.
	Enforcement string `yaml:"enforcement,omitempty"`

	// Landing is how a change lands on this class: direct (pushed) or merge_request
	// (a merge request, which GitHub calls a pull request).
	Landing string `yaml:"landing,omitempty"`

	// Protect holds the push-time rules the forge enforces.
	Protect BranchProtect `yaml:"protect,omitempty"`

	// Review holds the approvals a merge request needs.
	Review BranchReview `yaml:"review,omitempty"`

	// Checks holds the StageFreight phases that must pass before a merge.
	Checks BranchChecks `yaml:"checks,omitempty"`

	// Merge is how a merge request lands: merge, semi_linear, fast_forward, squash or rebase.
	Merge string `yaml:"merge,omitempty"`

	// Queue lands merge requests through a merge queue (GitLab merge trains), so two
	// merge requests that pass alone can't break the class together.
	Queue bool `yaml:"queue,omitempty"`

	// ForkChanges is how changes from forks are treated: untrusted (default; validated
	// without secrets) or trusted.
	ForkChanges string `yaml:"fork_changes,omitempty"`

	// Bypass lists who may get past these rules, each with a mode and a reason. Every
	// entry weakens the guarantee the rules give, so each one is named and justified.
	Bypass []WorkflowBypass `yaml:"bypass,omitempty"`
}

// BranchProtect is the push-time rules: what the forge refuses before a push lands.
// Unset means not governed.
type BranchProtect struct {
	ForcePush     *bool `yaml:"force_push,omitempty"`
	Deletion      *bool `yaml:"deletion,omitempty"`
	SignedCommits *bool `yaml:"signed_commits,omitempty"`
}

// BranchReview is the approvals a merge request needs.
type BranchReview struct {
	Approvals                 int  `yaml:"approvals,omitempty"`
	CodeOwners                bool `yaml:"code_owners,omitempty"`
	DismissStale              bool `yaml:"dismiss_stale,omitempty"`
	RequireRecentPushApproval bool `yaml:"require_recent_push_approval,omitempty"`
	AuthorCannotApprove       bool `yaml:"author_cannot_approve,omitempty"`
	ConversationResolved      bool `yaml:"conversation_resolved,omitempty"`
}

// BranchChecks is the phases that must pass before a merge, and whether the merge
// request must be up to date with its target first.
type BranchChecks struct {
	Required []string `yaml:"required,omitempty"`
	UpToDate bool     `yaml:"up_to_date,omitempty"`
}

// WorkflowBypass is one actor allowed past a class's rules.
type WorkflowBypass struct {
	// Actor is a forge user, group or app, or stagefreight for StageFreight's own pushes.
	Actor string `yaml:"actor"`
	// Mode is always, or merge_request (only through a merge request).
	Mode string `yaml:"mode"`
	// Reason says why the bypass exists.
	Reason string `yaml:"reason"`
}

// Promotion is one way a change moves from one class to another.
type Promotion struct {
	ID string `yaml:"-"`

	From string `yaml:"from"` // class id
	To   string `yaml:"to"`   // class id

	// Method is merge_request, fast_forward, cherry_pick or tag.
	Method string `yaml:"method"`

	// Schedule names a schedules: entry when the promotion runs on a cadence (a release
	// train) instead of per change.
	Schedule string `yaml:"schedule,omitempty"`

	// Require holds what a change needs before it promotes.
	Require PromotionRequire `yaml:"require,omitempty"`
}

// PromotionRequire is the criteria a change meets before it promotes.
type PromotionRequire struct {
	Checks    []string `yaml:"checks,omitempty"`
	Approvals int      `yaml:"approvals,omitempty"`
}

// WorkflowRelease declares where release tags may be cut.
type WorkflowRelease struct {
	// From maps a class id to the release kinds its branches may be tagged with:
	// prerelease, major, minor, patch.
	From map[string][]string `yaml:"from,omitempty"`
}

// Workflow vocabularies.
const (
	LifetimeLong     = "long"
	LifetimeShort    = "short"
	LifetimeOnDemand = "on_demand"

	EnforcementActive   = "active"
	EnforcementEvaluate = "evaluate"
	EnforcementOff      = "off"

	LandingDirect       = "direct"
	LandingMergeRequest = "merge_request"

	PromoteMergeRequest = "merge_request"
	PromoteFastForward  = "fast_forward"
	PromoteCherryPick   = "cherry_pick"
	PromoteTag          = "tag"

	CreatedFromTag = "tag"
)

var (
	workflowLifetimes    = []string{LifetimeLong, LifetimeShort, LifetimeOnDemand}
	workflowEnforcements = []string{EnforcementActive, EnforcementEvaluate, EnforcementOff}
	workflowLandings     = []string{LandingDirect, LandingMergeRequest}
	workflowMerges       = []string{"merge", "semi_linear", "fast_forward", "squash", "rebase"}
	workflowForkChanges  = []string{"untrusted", "trusted"}
	workflowBypassModes  = []string{"always", "merge_request"}
	workflowMethods      = []string{PromoteMergeRequest, PromoteFastForward, PromoteCherryPick, PromoteTag}
	workflowReleaseKinds = []string{"prerelease", "major", "minor", "patch"}

	// workflowCheckPhases are the phases that run before a merge. publish and narrate run
	// after a change has landed, so a merge can't wait on them.
	workflowCheckPhases = []string{"audition", "perform", "review"}
)

// OrderedBranchClasses is workflow.branches — an id→class map (key becomes BranchClass.ID).
type OrderedBranchClasses []BranchClass

func (OrderedBranchClasses) isIDMap() {}

func (o *OrderedBranchClasses) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(c *BranchClass, id string) { c.ID = id })
	if err != nil {
		return fmt.Errorf("workflow.branches: %w", err)
	}
	*o = v
	return nil
}

// OrderedPromotions is workflow.promote — an id→promotion map (key becomes Promotion.ID).
type OrderedPromotions []Promotion

func (OrderedPromotions) isIDMap() {}

func (o *OrderedPromotions) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(p *Promotion, id string) { p.ID = id })
	if err != nil {
		return fmt.Errorf("workflow.promote: %w", err)
	}
	*o = v
	return nil
}

// validateWorkflow checks the workflow: block's vocabularies and that every reference
// resolves: a class to a git.branches matcher, a promotion to declared classes and
// schedules, a release entry to a declared class.
func validateWorkflow(cfg *Config) []string {
	w := cfg.Workflow
	var errs []string
	oneOf := func(path, v string, allowed []string) {
		if v != "" && !slices.Contains(allowed, v) {
			errs = append(errs, fmt.Sprintf("%s: unknown %q (%s)", path, v, strings.Join(allowed, " | ")))
		}
	}
	checks := func(path string, phases []string) {
		for _, p := range phases {
			if !slices.Contains(workflowCheckPhases, p) {
				errs = append(errs, fmt.Sprintf("%s: %q is not a phase that runs before a merge (%s)", path, p, strings.Join(workflowCheckPhases, " | ")))
			}
		}
	}

	classes := make(map[string]bool, len(w.Branches))
	for _, c := range w.Branches {
		classes[c.ID] = true
	}
	matchedBy := make(map[string]string, len(w.Branches))

	for _, c := range w.Branches {
		path := "workflow.branches." + c.ID
		if !isIdentifier(c.ID) {
			errs = append(errs, fmt.Sprintf("%s: id must match [a-zA-Z][a-zA-Z0-9_.-]*", path))
		}
		switch {
		case c.Match == "":
			errs = append(errs, path+".match: required (a git.branches matcher)")
		case cfg.Git.Branches[c.Match] == "":
			errs = append(errs, fmt.Sprintf("%s.match: %q is not a git.branches matcher", path, c.Match))
		case matchedBy[c.Match] != "":
			errs = append(errs, fmt.Sprintf("%s.match: %q already selects class %q; a branch belongs to one class", path, c.Match, matchedBy[c.Match]))
		default:
			matchedBy[c.Match] = c.ID
		}

		oneOf(path+".lifetime", c.Lifetime, workflowLifetimes)
		if c.MaxAge != "" {
			if c.Lifetime != LifetimeShort {
				errs = append(errs, path+".max_age: only a short-lived class has a maximum age")
			} else if d, err := ParseDuration(c.MaxAge); err != nil || d <= 0 {
				errs = append(errs, fmt.Sprintf("%s.max_age: %q is not a positive duration (e.g. 14d, 72h)", path, c.MaxAge))
			}
		}
		switch {
		case c.Lifetime == LifetimeOnDemand && c.CreatedFrom == "":
			errs = append(errs, path+".created_from: required for an on_demand class (a class id, or tag)")
		case c.CreatedFrom != "" && c.Lifetime != LifetimeOnDemand:
			errs = append(errs, path+".created_from: only an on_demand class is cut from something")
		case c.CreatedFrom != "" && c.CreatedFrom != CreatedFromTag && !classes[c.CreatedFrom]:
			errs = append(errs, fmt.Sprintf("%s.created_from: %q is neither tag nor a declared class", path, c.CreatedFrom))
		case c.CreatedFrom == c.ID:
			errs = append(errs, path+".created_from: a class can't be cut from itself")
		}

		oneOf(path+".enforcement", c.Enforcement, workflowEnforcements)
		oneOf(path+".landing", c.Landing, workflowLandings)
		oneOf(path+".merge", c.Merge, workflowMerges)
		oneOf(path+".fork_changes", c.ForkChanges, workflowForkChanges)
		if c.Review.Approvals < 0 {
			errs = append(errs, path+".review.approvals: can't be negative")
		}
		checks(path+".checks.required", c.Checks.Required)

		// Merge-request rules mean nothing on a class changes are pushed to directly.
		if c.Landing == LandingDirect {
			if c.Review != (BranchReview{}) {
				errs = append(errs, path+".review: set, but landing is direct; review applies to merge requests")
			}
			if len(c.Checks.Required) > 0 || c.Checks.UpToDate {
				errs = append(errs, path+".checks: set, but landing is direct; checks gate merge requests")
			}
			if c.Merge != "" || c.Queue {
				errs = append(errs, path+": merge and queue are set, but landing is direct")
			}
		}

		for i, b := range c.Bypass {
			bpath := fmt.Sprintf("%s.bypass[%d]", path, i)
			if strings.TrimSpace(b.Actor) == "" {
				errs = append(errs, bpath+".actor: required")
			}
			if b.Mode == "" {
				errs = append(errs, bpath+".mode: required ("+strings.Join(workflowBypassModes, " | ")+")")
			} else {
				oneOf(bpath+".mode", b.Mode, workflowBypassModes)
			}
			if strings.TrimSpace(b.Reason) == "" {
				errs = append(errs, bpath+".reason: required; every bypass weakens the rules, so say why it exists")
			}
		}
	}

	for _, p := range w.Promote {
		path := "workflow.promote." + p.ID
		if !isIdentifier(p.ID) {
			errs = append(errs, fmt.Sprintf("%s: id must match [a-zA-Z][a-zA-Z0-9_.-]*", path))
		}
		for _, end := range []struct{ field, id string }{{"from", p.From}, {"to", p.To}} {
			switch {
			case end.id == "":
				errs = append(errs, fmt.Sprintf("%s.%s: required (a workflow.branches class)", path, end.field))
			case !classes[end.id]:
				errs = append(errs, fmt.Sprintf("%s.%s: %q is not a workflow.branches class", path, end.field, end.id))
			}
		}
		if p.From != "" && p.From == p.To {
			errs = append(errs, path+": from and to are the same class")
		}
		if p.Method == "" {
			errs = append(errs, path+".method: required ("+strings.Join(workflowMethods, " | ")+")")
		} else {
			oneOf(path+".method", p.Method, workflowMethods)
		}
		if p.Method == PromoteFastForward {
			for _, c := range w.Branches {
				if c.ID == p.To && c.Landing == LandingMergeRequest {
					errs = append(errs, fmt.Sprintf("%s.method: %s takes changes through merge requests, so it can't be fast-forwarded to", path, p.To))
				}
			}
		}
		if p.Schedule != "" {
			if _, ok := findSchedule(cfg.Schedules, p.Schedule); !ok {
				errs = append(errs, fmt.Sprintf("%s.schedule: %q is not a declared schedule", path, p.Schedule))
			}
		}
		if p.Require.Approvals < 0 {
			errs = append(errs, path+".require.approvals: can't be negative")
		}
		checks(path+".require.checks", p.Require.Checks)
	}

	releaseClasses := make([]string, 0, len(w.Release.From))
	for class := range w.Release.From {
		releaseClasses = append(releaseClasses, class)
	}
	sort.Strings(releaseClasses)
	for _, class := range releaseClasses {
		kinds := w.Release.From[class]
		path := "workflow.release.from." + class
		if !classes[class] {
			errs = append(errs, fmt.Sprintf("%s: %q is not a workflow.branches class", path, class))
		}
		if len(kinds) == 0 {
			errs = append(errs, path+": list the release kinds this class may carry ("+strings.Join(workflowReleaseKinds, " | ")+")")
		}
		for _, k := range kinds {
			oneOf(path, k, workflowReleaseKinds)
		}
	}
	return errs
}
