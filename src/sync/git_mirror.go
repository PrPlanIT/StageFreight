package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
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
func MirrorPush(ctx context.Context, worktree string, mirror config.ResolvedRepo, refCtx RefContext) (*MirrorResult, error) {
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

	refSpecs := buildPushRefSpecs(localRefs, remoteRefs, mirror.Sync.Branches, mirror.Sync.Tags, refCtx)

	if len(refSpecs) == 0 {
		result.Status = SyncSuccess
		result.Message = "no refs to push"
		result.Duration = time.Since(start)
		return result, nil
	}

	pushErr := bareRepo.PushContext(ctx, &git.PushOptions{
		RemoteName: "mirror",
		RefSpecs:   refSpecs,
		Auth:       mirrorAuth,
		Force:      true,
	})

	result.Duration = time.Since(start)

	if pushErr != nil && pushErr != git.NoErrAlreadyUpToDate {
		result.Status = SyncFailed
		result.Degraded = true
		result.FailureReason = classifyGoGitFailure(pushErr)
		result.Message = sanitizeError(pushErr)
		return result, nil
	}

	result.Status = SyncSuccess
	result.Message = fmt.Sprintf("mirror push to %s succeeded", mirror.ID)
	return result, nil
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

// collectLocalRefs enumerates heads and tags in the local bare repo.
func collectLocalRefs(repo *git.Repository) (map[string]bool, error) {
	refs, err := repo.References()
	if err != nil {
		return nil, err
	}

	local := make(map[string]bool)
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().String()
		if strings.HasPrefix(name, "refs/heads/") || strings.HasPrefix(name, "refs/tags/") {
			local[name] = true
		}
		return nil
	})
	return local, err
}

// listRemoteRefs queries the mirror remote for its current refs.
func listRemoteRefs(ctx context.Context, repo *git.Repository, auth transport.AuthMethod) (map[string]bool, error) {
	remote, err := repo.Remote("mirror")
	if err != nil {
		return nil, err
	}

	remoteRefList, err := remote.ListContext(ctx, &git.ListOptions{Auth: auth})
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		// A freshly-created mirror has no refs yet — this is the bootstrap case,
		// not a failure. Return an empty ref set so the caller force-pushes every
		// local head + tag to populate it (nothing to prune).
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}

	refs := make(map[string]bool)
	for _, ref := range remoteRefList {
		name := ref.Name().String()
		if strings.HasPrefix(name, "refs/heads/") || strings.HasPrefix(name, "refs/tags/") {
			refs[name] = true
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

// buildPushRefSpecs builds push/prune refspecs honoring each facet's scope. The
// branches and tags FacetSpecs each drive their own ref class independently; a
// nil facet means that ref class is not touched at all (not pushed, not pruned).
//
//	scope: current → only refCtx's ref of that class, never prune
//	scope: all     → all local refs of that class, add-only
//	prune (exact)  → also delete remote refs of that class absent from the push set
//
// gh-pages is never pruned — it is a deploy branch created on the mirror, not the
// source, so a pruning mirror must not wipe the published Pages site.
func buildPushRefSpecs(local, remote map[string]bool, branches, tags *config.FacetSpec, refCtx RefContext) []gitconfig.RefSpec {
	var specs []gitconfig.RefSpec
	specs = append(specs, facetRefSpecs("refs/heads/", local, remote, branches, refCtx.Branch)...)
	specs = append(specs, facetRefSpecs("refs/tags/", local, remote, tags, refCtx.Tag)...)
	return specs
}

// facetRefSpecs builds the push+prune refspecs for one ref class (heads or tags)
// under a single facet spec. currentRef is the short name (no prefix) of the ref
// this run addresses, "" if none.
func facetRefSpecs(prefix string, local, remote map[string]bool, spec *config.FacetSpec, currentRef string) []gitconfig.RefSpec {
	if spec == nil {
		return nil // facet not synced — leave this ref class untouched
	}

	// Select which local refs to push.
	pushSet := make(map[string]bool)
	for _, name := range sortedKeys(local) {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		short := strings.TrimPrefix(name, prefix)
		if !facetMatches(spec, short) {
			continue
		}
		if spec.IsCurrent() {
			if currentRef != "" && short == currentRef {
				pushSet[name] = true
			}
			continue
		}
		pushSet[name] = true // scope: all
	}

	var specs []gitconfig.RefSpec
	for _, name := range sortedKeys(pushSet) {
		specs = append(specs, gitconfig.RefSpec("+"+name+":"+name))
	}

	// Prune only under exact (spec.Prune): delete remote refs of this class that
	// are not in the push set. Never prune gh-pages, and only prune within the
	// match filter (a scoped prune must not reach outside its own selection).
	if spec.Prune {
		for _, name := range sortedKeys(remote) {
			if !strings.HasPrefix(name, prefix) || pushSet[name] {
				continue
			}
			if name == "refs/heads/gh-pages" {
				continue
			}
			if !facetMatches(spec, strings.TrimPrefix(name, prefix)) {
				continue
			}
			specs = append(specs, gitconfig.RefSpec(":"+name))
		}
	}

	return specs
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

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
		strings.Contains(msg, "not found") ||
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
