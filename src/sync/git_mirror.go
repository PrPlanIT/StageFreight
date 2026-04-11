package sync

import (
	"context"
	"fmt"
	"os"
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
func MirrorPush(ctx context.Context, worktree string, mirror config.ResolvedRepo) (*MirrorResult, error) {
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

	refSpecs := buildPushRefSpecs(localRefs, remoteRefs)

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

// buildPushRefSpecs builds force-push refspecs for all local refs and
// delete refspecs for remote refs not present locally (prune).
// This is equivalent to: git push --prune --force --all + --tags
// Refs are sorted for deterministic ordering in logs and debugging.
func buildPushRefSpecs(local, remote map[string]bool) []gitconfig.RefSpec {
	var specs []gitconfig.RefSpec

	// Force-push all local heads + tags (sorted)
	for _, name := range sortedKeys(local) {
		specs = append(specs, gitconfig.RefSpec("+"+name+":"+name))
	}

	// Prune: delete refs that exist on remote but not locally (sorted)
	for _, name := range sortedKeys(remote) {
		if !local[name] {
			specs = append(specs, gitconfig.RefSpec(":"+name))
		}
	}

	return specs
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
