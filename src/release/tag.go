package release

import (
	"fmt"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

// TagPlan is the fully resolved release tag plan.
type TagPlan struct {
	PreviousTag string
	TargetRef   string
	TargetSHA   string
	// SkippedTipSHA is the original tip commit when it was a [skip ci] commit and the
	// tag was walked back to a releasable ancestor (TargetSHA). Empty when the tip was
	// already releasable. Surfaced so the CLI can narrate the substitution.
	SkippedTipSHA string
	NextTag       string
	Message       string
	CommitCount   int
	FilesChanged  int
	Insertions    int
	Deletions     int
}

// BuildTagPlanOptions configures tag plan resolution.
type BuildTagPlanOptions struct {
	ExplicitVersion string
	BumpKind        string // patch | minor | major
	TargetRef       string // default HEAD
	FromRef         string // optional previous boundary override
	MessageOverride string
	TagPatterns     []string // from versioning.tag_sources
	Glossary        config.GlossaryConfig
	Presentation    config.TagPresentation
}

// BuildTagPlan resolves a complete tag plan from the repo and options.
func BuildTagPlan(repoDir string, opts BuildTagPlanOptions) (*TagPlan, error) {
	plan := &TagPlan{}

	// 1. Resolve target ref
	targetRef := opts.TargetRef
	if targetRef == "" {
		targetRef = "HEAD"
	}
	plan.TargetRef = targetRef

	sha, err := ResolveGitRef(repoDir, targetRef)
	if err != nil {
		return nil, fmt.Errorf("resolving target ref %q: %w", targetRef, err)
	}
	// A release tag must never land on a [skip ci] commit (e.g. an auto-docs tip): the
	// tag pipeline would be suppressed and nothing would build or publish. Walk back to
	// the nearest releasable ancestor and tag — and range from — that instead.
	effectiveSHA, skippedTip, err := resolveReleasableCommit(repoDir, sha)
	if err != nil {
		return nil, err
	}
	plan.TargetSHA = effectiveSHA
	plan.SkippedTipSHA = skippedTip
	// Everything downstream (previous-tag search, range stats, changelog) hangs off the
	// releasable commit, so the skipped docs tip is excluded from the notes too. When no
	// skip occurred this resolves to the same commit as targetRef — no behavior change.
	rangeRef := effectiveSHA

	// 2. Find previous release tag
	if opts.FromRef != "" {
		plan.PreviousTag = opts.FromRef
	} else {
		prev, err := PreviousReleaseTag(repoDir, rangeRef, opts.TagPatterns)
		if err != nil {
			// No previous tag is OK for first release
			plan.PreviousTag = ""
		} else {
			plan.PreviousTag = prev
		}
	}

	// 3. Resolve next version
	if opts.ExplicitVersion != "" {
		plan.NextTag = opts.ExplicitVersion
	} else if opts.BumpKind != "" {
		if plan.PreviousTag == "" {
			return nil, fmt.Errorf("cannot bump %s: no previous release tag found", opts.BumpKind)
		}
		// Validate --from is a release tag when bumping
		if opts.FromRef != "" {
			isRelease := false
			for _, pattern := range opts.TagPatterns {
				if config.MatchPatterns([]string{pattern}, opts.FromRef) {
					isRelease = true
					break
				}
			}
			if !isRelease {
				return nil, fmt.Errorf("cannot bump from %q: not a release tag (does not match any git_tags policy)", opts.FromRef)
			}
		}
		next, err := BumpVersion(plan.PreviousTag, opts.BumpKind)
		if err != nil {
			return nil, err
		}
		plan.NextTag = next
	}

	// 4. Check tag doesn't already exist
	if plan.NextTag != "" {
		if tagExists(repoDir, plan.NextTag) {
			return nil, fmt.Errorf("tag %q already exists", plan.NextTag)
		}
	}

	// 5. Generate commit range stats
	if plan.PreviousTag != "" {
		plan.CommitCount = countCommits(repoDir, plan.PreviousTag, rangeRef)
		stats := diffStats(repoDir, plan.PreviousTag, rangeRef)
		plan.FilesChanged = stats.files
		plan.Insertions = stats.insertions
		plan.Deletions = stats.deletions
	}

	// 6. Generate message
	if opts.MessageOverride != "" {
		plan.Message = opts.MessageOverride
	} else {
		commits, _ := ParseCommits(repoDir, plan.PreviousTag, rangeRef)
		processed := ProcessCommits(commits, opts.Glossary)
		plan.Message = FormatHighlights(processed, opts.Presentation.MaxEntries)
	}

	return plan, nil
}

// ResolveGitRef resolves any git ref to a commit SHA.
func ResolveGitRef(repoDir, ref string) (string, error) {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	return gitstate.ResolveRef(repo, ref)
}

// ciSkipTokens are commit-message markers that suppress a CI pipeline on the major forges
// (GitLab, GitHub Actions). A release tag on a commit carrying one is skipped — the tag
// pipeline never runs, so nothing builds or publishes. This is why main's auto-docs tip
// (which appends [skip ci]) must never be the tag target.
var ciSkipTokens = []string{"[skip ci]", "[ci skip]", "[no ci]", "[skip actions]", "[actions skip]", "skip-ci", "ci-skip"}

// MessageSkipsCI reports whether a commit message contains a CI-skip marker.
func MessageSkipsCI(msg string) bool {
	low := strings.ToLower(msg)
	for _, t := range ciSkipTokens {
		if strings.Contains(low, t) {
			return true
		}
	}
	return false
}

// resolveReleasableCommit walks first-parents from sha to the nearest commit whose message
// does NOT skip CI. Returns that commit and — when the tip itself skipped CI — the original
// tip SHA (else ""). Bounded so a pathological all-skip range cannot loop; on any read
// failure or an exhausted bound it falls back to the tip (better a tag that might not build
// than a release command that errors out on the operator).
// tipIsReleasable reports whether a commit may anchor a release tag. It excludes two
// kinds of tip that produce no buildable release: a legacy/manual [skip ci] commit, and a
// narrate commit (Generated-By: StageFreight) that only regenerates docs/badges. A deps
// commit (Updated-By: StageFreight) DOES rebuild the image, so it stays releasable — the
// exclusion is deliberately narrow to Generated-By, not any StageFreight-authored commit.
func tipIsReleasable(msg string) bool {
	return !MessageSkipsCI(msg) && !strings.Contains(msg, config.GeneratedByTrailer)
}

func resolveReleasableCommit(repoDir, sha string) (effective, skippedTip string, err error) {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return "", "", fmt.Errorf("opening repo: %w", err)
	}
	const maxWalk = 50
	tip := sha
	h := plumbing.NewHash(sha)
	for i := 0; i < maxWalk; i++ {
		c, cerr := repo.CommitObject(h)
		if cerr != nil {
			return tip, "", nil // unreadable history → tag the tip, do not fail the release
		}
		if tipIsReleasable(c.Message) {
			if h.String() == tip {
				return tip, "", nil // tip is already releasable
			}
			return h.String(), tip, nil
		}
		if len(c.ParentHashes) == 0 {
			break
		}
		h = c.ParentHashes[0] // follow first-parent (mainline)
	}
	return tip, "", nil // no releasable ancestor within bound → tag the tip
}

// CreateAnnotatedTag creates an annotated git tag on a specific commit.
func CreateAnnotatedTag(repoDir, tag, targetSHA, message string) error {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return fmt.Errorf("opening repo: %w", err)
	}
	hash := plumbing.NewHash(targetSHA)
	_, err = repo.CreateTag(tag, hash, &git.CreateTagOptions{
		Tagger:  resolveTaggerSignature(repo),
		Message: message,
	})
	if err != nil {
		return fmt.Errorf("creating tag %s: %w", tag, err)
	}
	return nil
}

// PushTag pushes a tag to the given remote.
func PushTag(repoDir, remote, tag string) error {
	session, err := gitstate.OpenSyncSession(repoDir)
	if err != nil {
		return fmt.Errorf("opening sync session: %w", err)
	}
	refspec := "refs/tags/" + tag + ":refs/tags/" + tag
	if err := session.Push(remote, refspec, false); err != nil {
		return fmt.Errorf("pushing tag %s to %s: %w", tag, remote, err)
	}
	return nil
}

// tagExists checks if a git tag already exists.
func tagExists(repoDir, tag string) bool {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return false
	}
	_, err = gitstate.ResolveRef(repo, "refs/tags/"+tag)
	return err == nil
}

// countCommits returns the number of commits in a range.
func countCommits(repoDir, from, to string) int {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return 0
	}
	n, _ := gitstate.CountCommitsBetween(repo, from, to)
	return n
}

type diffStatsResult struct {
	files      int
	insertions int
	deletions  int
}

// diffStats returns diff statistics for a range.
func diffStats(repoDir, from, to string) diffStatsResult {
	repo, err := gitstate.OpenRepo(repoDir)
	if err != nil {
		return diffStatsResult{}
	}
	files, insertions, deletions, err := gitstate.DiffStats(repo, from, to)
	if err != nil {
		return diffStatsResult{}
	}
	return diffStatsResult{files: files, insertions: insertions, deletions: deletions}
}

// resolveTaggerSignature resolves the git user identity for tag signing.
// Resolution order: local config → global config → built-in defaults.
func resolveTaggerSignature(repo *git.Repository) *object.Signature {
	name, email := "stagefreight", "stagefreight@localhost"
	if cfg, err := repo.Config(); err == nil {
		if cfg.User.Name != "" {
			name = cfg.User.Name
		}
		if cfg.User.Email != "" {
			email = cfg.User.Email
		}
	}
	if name == "stagefreight" || email == "stagefreight@localhost" {
		if global, err := gitconfig.LoadConfig(gitconfig.GlobalScope); err == nil {
			if global.User.Name != "" && name == "stagefreight" {
				name = global.User.Name
			}
			if global.User.Email != "" && email == "stagefreight@localhost" {
				email = global.User.Email
			}
		}
	}
	return &object.Signature{Name: name, Email: email, When: time.Now()}
}
