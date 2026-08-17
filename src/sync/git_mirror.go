package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/mirror"
)

// resolveGitAuth maps a provider and secret to the correct git transport
// username/password pair. This is the ONLY place provider-specific username
// rules live — do not duplicate elsewhere.
func resolveGitAuth(provider, secret string) *githttp.BasicAuth {
	switch provider {
	case "github":
		return &githttp.BasicAuth{Username: "x-access-token", Password: secret}
	case "gitlab":
		return &githttp.BasicAuth{Username: "oauth2", Password: secret}
	default:
		return &githttp.BasicAuth{Username: "git", Password: secret}
	}
}

// buildRemoteURL constructs a plain HTTPS URL for the mirror remote.
func buildRemoteURL(repo config.ResolvedRepo) string {
	baseURL := strings.TrimRight(repo.BaseURL, "/")
	projectPath := strings.TrimLeft(repo.Project, "/")
	u := baseURL + "/" + projectPath
	if !strings.HasSuffix(u, ".git") {
		u += ".git"
	}
	return u
}

// MirrorPush performs an authoritative git mirror push from the primary
// forge (origin) to a mirror forge using go-git. Clones from origin into
// a temp bare repo and pushes all heads + tags with force.
//
// Invariants:
//   - Never mutates the user's working repo (temp bare clone only)
//   - Credentials passed via go-git BasicAuth, never in URLs
//   - No git binary required
func MirrorPush(ctx context.Context, worktree string, mirror config.ResolvedRepo, refCtx RefContext, rollingAliases map[string]bool) (*MirrorResult, error) {
	start := time.Now()
	result := &MirrorResult{
		AccessoryID: mirror.ID,
	}

	// Resolve the origin remote URL from the worktree.
	originURL, err := resolveOriginURL(ctx, worktree)
	if err != nil {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = MirrorUnknown
		result.Message = fmt.Sprintf("failed to resolve origin URL: %v", err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// Resolve origin auth (SSH for GitLab/local, may be nil for public repos)
	originAuth, err := resolveCloneAuth(originURL)
	if err != nil {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = MirrorAuthFailed
		result.Message = fmt.Sprintf("failed to resolve origin auth: %v", err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 1. Clone from origin into a temp bare repo.
	tmpDir, err := os.MkdirTemp("", "sf-mirror-*")
	if err != nil {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = MirrorUnknown
		result.Message = fmt.Sprintf("failed to create temp directory: %v", err)
		result.Duration = time.Since(start)
		return result, nil
	}
	defer os.RemoveAll(tmpDir)

	cloneOpts := &git.CloneOptions{
		URL:    originURL,
		Auth:   originAuth,
		Mirror: true,
	}

	bareRepo, err := git.PlainCloneContext(ctx, tmpDir, true, cloneOpts)
	if err != nil {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = classifyGoGitFailure(err)
		result.Message = fmt.Sprintf("failed to clone from origin: %v", sanitizeError(err))
		result.Duration = time.Since(start)
		return result, nil
	}

	// 2. Resolve mirror credentials.
	creds := credentials.ResolvePrefix(mirror.Credentials)
	if creds.Secret == "" {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = MirrorAuthFailed
		result.Message = fmt.Sprintf("no secret resolved for credentials prefix %q", mirror.Credentials)
		result.Duration = time.Since(start)
		return result, nil
	}

	mirrorAuth := resolveGitAuth(mirror.Provider, creds.Secret)
	remoteURL := buildRemoteURL(mirror)

	// 3. Add the mirror as a remote and push all refs.
	_, err = bareRepo.CreateRemote(&gitconfig.RemoteConfig{
		Name: "mirror",
		URLs: []string{remoteURL},
	})
	if err != nil {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = MirrorUnknown
		result.Message = fmt.Sprintf("failed to add mirror remote: %v", err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// Build refspecs: force-push local heads + tags, delete remote-only refs (prune).
	// Scope: heads + tags only. NOT --mirror push (breaks GitHub default branch).
	// Original code: git push --prune --force --all + git push --prune --force --tags
	localRefs, err := collectLocalRefs(bareRepo)
	if err != nil {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = MirrorUnknown
		result.Message = fmt.Sprintf("failed to enumerate local refs: %v", err)
		result.Duration = time.Since(start)
		return result, nil
	}

	remoteRefs, err := listRemoteRefs(ctx, bareRepo, mirrorAuth)
	if err != nil {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = classifyGoGitFailure(err)
		result.Message = fmt.Sprintf("failed to list mirror refs: %v", sanitizeError(err))
		result.Duration = time.Since(start)
		return result, nil
	}

	refPlan := buildPushRefSpecs(localRefs, remoteRefs, mirror.Sync.Branches, mirror.Sync.Tags, refCtx, rollingAliases, ancestryChecker(bareRepo))

	if len(refPlan.specs) == 0 {
		result.Status = SyncSuccess
		result.Message = refPlanSummary("no refs to push", refPlan)
		result.Duration = time.Since(start)
		return result, nil
	}

	// Force:false — divergence protection lives PER-REFSPEC (the "+" prefix), set
	// only for force-opted facets. A blanket Force here would clobber every
	// diverged mirror ref, which is exactly the footgun keep-divergent prevents.
	pushErr := bareRepo.PushContext(ctx, &git.PushOptions{
		RemoteName: "mirror",
		RefSpecs:   refPlan.specs,
		Auth:       mirrorAuth,
		Force:      false,
	})

	result.Duration = time.Since(start)

	if pushErr != nil && pushErr != git.NoErrAlreadyUpToDate {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason, result.Message = classifyPushFailure(pushErr, refPlan)
		return result, nil
	}

	result.Status = SyncSuccess
	result.Message = refPlanSummary(fmt.Sprintf("mirror push to %s succeeded", mirror.ID), refPlan)
	return result, nil
}

// refPlanSummary appends what the plan surfaced but did not touch — so a human
// sees foreign refs left alone and any prune, not just "succeeded".
func refPlanSummary(base string, plan refPushPlan) string {
	var notes []string
	if len(plan.pruned) > 0 {
		notes = append(notes, fmt.Sprintf("%d pruned", len(plan.pruned)))
	}
	if len(plan.foreign) > 0 {
		notes = append(notes, fmt.Sprintf("%d foreign kept", len(plan.foreign)))
	}
	if len(plan.diverged) > 0 {
		notes = append(notes, fmt.Sprintf("%d diverged", len(plan.diverged)))
	}
	if len(notes) == 0 {
		return base
	}
	return base + " (" + strings.Join(notes, ", ") + ")"
}

// resolveOriginURL reads the origin remote URL from the worktree's git config.
func resolveOriginURL(_ context.Context, worktree string) (string, error) {
	repo, err := gitstate.OpenRepo(worktree)
	if err != nil {
		return "", fmt.Errorf("opening repo: %w", err)
	}
	u, err := gitstate.RemoteURL(repo, "origin")
	if err != nil {
		return "", fmt.Errorf("failed to resolve origin URL: %w", err)
	}
	if strings.TrimSpace(u) == "" {
		return "", fmt.Errorf("origin remote URL is empty")
	}
	return u, nil
}

// resolveCloneAuth resolves auth for cloning from origin.
// SSH URLs get SSH auth, HTTPS URLs get nil (public) or HTTP auth.
func resolveCloneAuth(originURL string) (transport.AuthMethod, error) {
	if gitstate.IsSSHURL(originURL) {
		return gitstate.ResolveAuth(originURL)
	}
	// HTTPS origin — typically public (the primary forge), no auth needed.
	return nil, nil
}

// collectLocalRefs enumerates heads and tags in the local bare repo, keyed to the
// COMMIT each ref ultimately names. An annotated tag is peeled to its target commit so
// its value can be compared against a lightweight tag (or an annotated tag on the mirror)
// that names the same commit — an annotated tag's own object hash differs from the commit
// hash, which otherwise reads as a false divergence. Peeling affects only the comparison
// value; the refspec pushes the ref by name, so the annotated tag object still transfers.
func collectLocalRefs(repo *git.Repository) (map[string]string, error) {
	refs, err := repo.References()
	if err != nil {
		return nil, err
	}

	local := make(map[string]string)
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().String()
		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			local[name] = ref.Hash().String()
		case strings.HasPrefix(name, "refs/tags/"):
			local[name] = peelToCommit(repo, ref.Hash())
		}
		return nil
	})
	return local, err
}

// ancestryChecker returns an IsAncestor closure over the local bare repo: it reports
// whether ancestor is an ancestor of descendant using go-git commit history. It returns
// false whenever either commit is not resolvable locally — a mirror commit absent from
// local history is itself the divergence signal, so no extra fetch is needed.
func ancestryChecker(repo *git.Repository) func(ancestor, descendant string) bool {
	return func(ancestor, descendant string) bool {
		aCommit, err := repo.CommitObject(plumbing.NewHash(ancestor))
		if err != nil {
			return false
		}
		dCommit, err := repo.CommitObject(plumbing.NewHash(descendant))
		if err != nil {
			return false
		}
		yes, err := aCommit.IsAncestor(dCommit)
		return err == nil && yes
	}
}

// peelToCommit resolves a ref hash to the commit it ultimately names: an annotated tag
// object is followed to its target commit; a lightweight tag or a direct commit ref
// resolves to itself. A tag pointing at a non-commit (or an unresolvable object) returns
// the direct target/hash rather than failing — comparison degrades to the raw hash rather
// than fabricating a match.
func peelToCommit(repo *git.Repository, h plumbing.Hash) string {
	tag, err := repo.TagObject(h)
	if err != nil {
		return h.String() // lightweight tag or plain commit ref — already a commit
	}
	if c, cErr := tag.Commit(); cErr == nil {
		return c.Hash.String()
	}
	return tag.Target.String()
}

// listRemoteRefs queries the mirror remote for its current refs (name → SHA).
// The SHAs are what let us tell a fast-forward from a true divergence.
func listRemoteRefs(ctx context.Context, repo *git.Repository, auth transport.AuthMethod) (map[string]string, error) {
	remote, err := repo.Remote("mirror")
	if err != nil {
		return nil, err
	}

	// AppendPeeled makes go-git surface the advertised peeled refs (refs/tags/x^{}) —
	// the target commit of an annotated tag — alongside the tag ref itself. Without it
	// an annotated tag advertises only its tag-object hash, which reads as a divergence
	// against a lightweight tag (or a differently-typed tag) naming the same commit.
	remoteRefList, err := remote.ListContext(ctx, &git.ListOptions{Auth: auth, PeelingOption: git.AppendPeeled})
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		// A freshly-created mirror has no refs yet — this is the bootstrap case,
		// not a failure. Return an empty ref set so every local head + tag is
		// created to populate it (nothing to prune, nothing to diverge).
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}

	// First pass: collect peeled commits by their base ref name (refs/tags/x^{} → refs/tags/x).
	peeled := make(map[string]string)
	for _, ref := range remoteRefList {
		name := ref.Name().String()
		if base, ok := strings.CutSuffix(name, "^{}"); ok {
			peeled[base] = ref.Hash().String()
		}
	}

	// Second pass: key each head/tag to its commit — the peeled commit when the ref is
	// an annotated tag, else its own hash (a lightweight tag or a branch is already a commit).
	refs := make(map[string]string)
	for _, ref := range remoteRefList {
		name := ref.Name().String()
		if strings.HasSuffix(name, "^{}") {
			continue // handled in the peeled pass
		}
		if strings.HasPrefix(name, "refs/heads/") || strings.HasPrefix(name, "refs/tags/") {
			if commit, ok := peeled[name]; ok {
				refs[name] = commit
			} else {
				refs[name] = ref.Hash().String()
			}
		}
	}
	return refs, nil
}

// RefContext is the ref the current run addresses — used by scope: current to
// pick the single branch/tag to replicate. Empty fields mean "no current ref of
// that kind" (Tag is empty on a branch build, Branch empty on a tag build).
type RefContext struct {
	Branch string // short branch name, "" if none
	Tag    string // short tag name, "" if not a tag run
}

// refPushPlan is the translated result for a whole mirror push: the refspecs to
// send, plus what was surfaced (never touched) for honest reporting.
type refPushPlan struct {
	specs    []gitconfig.RefSpec
	diverged []string // in-scope refs that differ and were NOT force-overwritten
	foreign  []string // mirror-only refs outside any declared scope — untouched
	pruned   []string // our (in-scope) mirror-only refs removed
}

func (p *refPushPlan) merge(o refPushPlan) {
	p.specs = append(p.specs, o.specs...)
	p.diverged = append(p.diverged, o.diverged...)
	p.foreign = append(p.foreign, o.foreign...)
	p.pruned = append(p.pruned, o.pruned...)
}

// buildPushRefSpecs builds push/prune refspecs honoring each facet's scope. The
// branches and tags FacetSpecs each drive their own ref class independently; a
// nil facet means that ref class is not touched at all (not pushed, not pruned).
//
//	scope: current → only refCtx's ref of that class, never prune
//	scope: all     → all local refs of that class
//	force          → overwrite a DIVERGED mirror ref; default off = keep-divergent
//	prune (exact)  → delete OUR mirror refs of that class absent from source, but
//	                 ONLY within a declared match scope (foreign refs are sacred)
func buildPushRefSpecs(local, remote map[string]string, branches, tags *config.FacetSpec, refCtx RefContext, rollingAliases map[string]bool, isAncestor func(ancestor, descendant string) bool) refPushPlan {
	var plan refPushPlan
	// Rolling aliases are a tag concept — heads never force-per-ref (nil). Ancestry
	// (fast-forward vs. true divergence) applies to both heads and tags.
	plan.merge(facetRefSpecs("refs/heads/", local, remote, branches, refCtx.Branch, nil, isAncestor))
	plan.merge(facetRefSpecs("refs/tags/", local, remote, tags, refCtx.Tag, rollingAliases, isAncestor))
	return plan
}

// facetRefSpecs builds the push/prune plan for one ref class (heads or tags)
// through the provenance-bounded PlanRefs engine. currentRef is the short name
// (no prefix) of the ref this run addresses, "" if none.
//
// Two safety invariants, both from PlanRefs:
//   - keep-divergent: a diverged ref is pushed WITHOUT force (a plain refspec),
//     so git fast-forwards it if it can and rejects a true divergence — nothing
//     is ever clobbered unless the facet opts into force.
//   - foreign is sacred: the prune ownership boundary is the facet's match glob.
//     With no match declared, a mirror-only ref is unattributable → foreign →
//     never pruned (a contributor's branch on the public mirror is never deleted).
//     gh-pages is always foreign — a deploy branch created on the mirror.
func facetRefSpecs(prefix string, local, remote map[string]string, spec *config.FacetSpec, currentRef string, rollingAliases map[string]bool, isAncestor func(ancestor, descendant string) bool) refPushPlan {
	var plan refPushPlan
	if spec == nil {
		return plan // facet not synced — leave this ref class untouched
	}

	// Rolling aliases (mutable by design, e.g. "latest") force-update even when the
	// facet keeps divergence, so a rolling tag that advanced upstream is not reported as
	// a perpetual divergence. Immutable version tags are absent from this set and keep
	// the default keep-divergent behavior.
	var forceRef func(string) bool
	if len(rollingAliases) > 0 {
		forceRef = func(short string) bool { return rollingAliases[short] }
	}

	// Source selection by SCOPE: which refs of this class we mirror at all.
	srcShort := map[string]string{}
	for full, sha := range local {
		if !strings.HasPrefix(full, prefix) {
			continue
		}
		short := strings.TrimPrefix(full, prefix)
		if !facetMatches(spec, short) {
			continue
		}
		if spec.IsCurrent() {
			if currentRef != "" && short == currentRef {
				srcShort[short] = sha
			}
			continue
		}
		srcShort[short] = sha // scope: all
	}

	mirShort := map[string]string{}
	for full, sha := range remote {
		if !strings.HasPrefix(full, prefix) {
			continue
		}
		mirShort[strings.TrimPrefix(full, prefix)] = sha
	}

	// The prune ownership boundary is the match glob. A declared match lets us
	// attribute a mirror-only ref as ours; with no match, InScope stays nil so
	// PlanRefs treats every mirror-only ref as foreign (never pruned). gh-pages
	// is excluded from scope regardless.
	var inScope func(string) bool
	if spec.Match != "" {
		inScope = func(short string) bool {
			return short != "gh-pages" && facetMatches(spec, short)
		}
	}

	rp := mirror.PlanRefs(srcShort, mirShort, mirror.RefOptions{
		Prune:      spec.Prune,
		Force:      spec.Force,
		ForceRef:   forceRef,
		IsAncestor: isAncestor,
		InScope:    inScope,
	})

	emit := func(short string, force bool) {
		full := prefix + short
		rs := full + ":" + full
		if force {
			rs = "+" + rs
		}
		plan.specs = append(plan.specs, gitconfig.RefSpec(rs))
	}
	for _, u := range rp.Create {
		emit(u.Ref, spec.Force)
	}
	for _, u := range rp.Update { // a forced overwrite (Force) or a non-force fast-forward
		emit(u.Ref, u.Force)
	}
	for _, name := range rp.Diverged {
		emit(name, false) // non-force: git fast-forwards or rejects, never clobbers
		plan.diverged = append(plan.diverged, prefix+name)
	}
	for _, u := range rp.Prune {
		plan.specs = append(plan.specs, gitconfig.RefSpec(":"+prefix+u.Ref))
		plan.pruned = append(plan.pruned, prefix+u.Ref)
	}
	for _, name := range rp.Foreign {
		plan.foreign = append(plan.foreign, prefix+name)
	}
	return plan
}

// facetMatches reports whether a ref's short name passes the facet's match glob
// (empty match = everything).
func facetMatches(spec *config.FacetSpec, short string) bool {
	if spec.Match == "" {
		return true
	}
	ok, err := path.Match(spec.Match, short)
	return err == nil && ok
}

// classifyPushFailure maps a push rejection to a failure reason and sanitized message. A
// rejection with kept-divergent refs IS the divergence — classified directly from the
// plan, so go-git's generic "object not found" wording never masquerades as another
// reason. Any other rejection falls through to best-effort error classification.
func classifyPushFailure(pushErr error, refPlan refPushPlan) (MirrorFailureReason, string) {
	if len(refPlan.diverged) > 0 {
		return MirrorDiverged, fmt.Sprintf(
			"mirror refs diverged, kept: %s — set sync force to overwrite",
			strings.Join(refPlan.diverged, ", "))
	}
	return classifyGoGitFailure(pushErr), sanitizeError(pushErr)
}

// classifyGoGitFailure performs best-effort classification of go-git errors.
func classifyGoGitFailure(err error) MirrorFailureReason {
	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "authentication") ||
		strings.Contains(msg, "invalid credentials") ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "403"):
		return MirrorAuthFailed

	case strings.Contains(msg, "protected branch") ||
		strings.Contains(msg, "pre-receive hook declined"):
		return MirrorProtectedRefRejected

	case strings.Contains(msg, "could not resolve host") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection timed out") ||
		strings.Contains(msg, "network is unreachable") ||
		strings.Contains(msg, "dial tcp"):
		return MirrorNetworkFailed

	case strings.Contains(msg, "repository not found") ||
		strings.Contains(msg, "404"):
		return MirrorRemoteNotFound

	case strings.Contains(msg, "rejected") ||
		strings.Contains(msg, "failed to push"):
		return MirrorPushRejected

	default:
		return MirrorUnknown
	}
}

// sanitizeError removes potential credential material from error messages.
func sanitizeError(err error) string {
	s := err.Error()
	if idx := strings.Index(s, "@"); idx > 0 {
		for _, scheme := range []string{"https://", "http://"} {
			if schemeIdx := strings.Index(s, scheme); schemeIdx >= 0 && schemeIdx < idx {
				s = s[:schemeIdx+len(scheme)] + "[redacted]" + s[idx:]
				break
			}
		}
	}
	return s
}
