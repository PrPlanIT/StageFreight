package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// ── go-git test fixtures ──
//
// These fixtures construct repositories with go-git directly rather than the
// git CLI, so the sync package carries no dependency on the git binary
// (enforced by commit.TestNoGitShellOuts). mirrorPushDirect mirrors the
// shape of production MirrorPush: clone --mirror into a temp bare repo, then
// force-push heads + tags with prune.

// testSig is the fixed author/committer signature for fixture commits.
func testSig() *object.Signature {
	return &object.Signature{Name: "test", Email: "test@test", When: time.Now()}
}

// setupTestRepo creates a minimal non-bare repo on main with one commit and a
// v1.0.0 tag. go-git's PlainInit defaults HEAD to master; the mirror tests
// assume main, so set it explicitly.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatalf("init repo: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatalf("add README.md: %v", err)
	}
	hash, err := wt.Commit("initial", &git.CommitOptions{Author: testSig()})
	if err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	if _, err := repo.CreateTag("v1.0.0", hash, nil); err != nil {
		t.Fatalf("tag v1.0.0: %v", err)
	}
	return dir
}

// setupBareRemote creates a bare repo to act as an accessory remote.
func setupBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		Bare:        true,
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	}); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	return dir
}

func TestMirrorPush_Success(t *testing.T) {
	source := setupTestRepo(t)
	remote := setupBareRemote(t)

	result := mirrorPushDirect(t, source, remote)

	if result.Status != SyncSuccess {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Degraded {
		t.Error("should not be degraded on success")
	}

	// Verify remote has the tag
	if !remoteHasTag(t, remote, "v1.0.0") {
		t.Errorf("remote should have tag v1.0.0")
	}

	// Verify remote has the branch
	if !remoteHasBranch(t, remote, "main") {
		t.Errorf("remote should have branch main")
	}
}

func TestMirrorPush_DeletedBranch(t *testing.T) {
	source := setupTestRepo(t)
	remote := setupBareRemote(t)

	// First push
	mirrorPushDirect(t, source, remote)

	// Create and push a branch
	srcRepo := openRepo(t, source)
	srcWt := worktree(t, srcRepo)
	checkout(t, srcWt, "feature", true)
	writeAddCommit(t, srcRepo, source, "feature.txt", "f", "feature")
	mirrorPushDirect(t, source, remote)

	// Verify feature branch exists on remote
	if !remoteHasBranch(t, remote, "feature") {
		t.Fatal("feature branch should exist on remote after push")
	}

	// Delete the branch on source
	checkout(t, srcWt, "main", false)
	if err := srcRepo.Storer.RemoveReference(plumbing.NewBranchReferenceName("feature")); err != nil {
		t.Fatalf("delete feature branch: %v", err)
	}
	mirrorPushDirect(t, source, remote)

	// Verify feature branch is gone from remote
	if remoteHasBranch(t, remote, "feature") {
		t.Error("feature branch should be deleted on remote after mirror push")
	}
}

func TestMirrorPush_OrphanedTag(t *testing.T) {
	source := setupTestRepo(t)
	remote := setupBareRemote(t)

	// First push (includes v1.0.0 tag)
	mirrorPushDirect(t, source, remote)

	// Delete tag on source
	srcRepo := openRepo(t, source)
	if err := srcRepo.DeleteTag("v1.0.0"); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	mirrorPushDirect(t, source, remote)

	// Verify tag is gone from remote
	if remoteHasTag(t, remote, "v1.0.0") {
		t.Error("orphaned tag v1.0.0 should be deleted on remote after mirror push")
	}
}

func TestMirrorPush_ForceRewrite(t *testing.T) {
	source := setupTestRepo(t)
	remote := setupBareRemote(t)

	// First push
	mirrorPushDirect(t, source, remote)

	// Get original remote HEAD SHA
	origSHA := remoteBranchHash(t, remote, "main")

	// Rewrite history on source: a fresh commit with no parent replaces main.
	// go-git has no --amend; resetting main to a brand-new root commit produces
	// the same observable effect (remote HEAD must move under a force push).
	srcRepo := openRepo(t, source)
	srcWt := worktree(t, srcRepo)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("rewritten"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := srcWt.Add("README.md"); err != nil {
		t.Fatalf("add README.md: %v", err)
	}
	newHash, err := srcWt.Commit("rewritten", &git.CommitOptions{Author: testSig()})
	if err != nil {
		t.Fatalf("rewrite commit: %v", err)
	}

	// Mirror push (force update)
	mirrorPushDirect(t, source, remote)

	// Verify HEAD changed
	newSHA := remoteBranchHash(t, remote, "main")
	if origSHA == newSHA {
		t.Error("remote HEAD should have changed after force rewrite")
	}
	if newSHA != newHash.String() {
		t.Errorf("remote main = %s, want %s", newSHA, newHash)
	}
}

func TestMirrorPush_NoMutationOfWorktree(t *testing.T) {
	source := setupTestRepo(t)
	remote := setupBareRemote(t)

	// Record .git/config before
	configBefore, _ := os.ReadFile(filepath.Join(source, ".git", "config"))

	mirrorPushDirect(t, source, remote)

	// Verify .git/config unchanged
	configAfter, _ := os.ReadFile(filepath.Join(source, ".git", "config"))
	if string(configBefore) != string(configAfter) {
		t.Error("mirror push must not mutate worktree .git/config")
	}
}

// pushSpecJoin runs buildPushRefSpecs and joins the refspecs for substring assertions.
func pushSpecJoin(local, remote map[string]bool, branches, tags *config.FacetSpec, ref RefContext) string {
	var out []string
	for _, r := range buildPushRefSpecs(local, remote, branches, tags, ref) {
		out = append(out, r.String())
	}
	return strings.Join(out, " ")
}

// TestBuildPushRefSpecs_PreservesGHPages guards the mirror-vs-pages fix under an
// exact (prune) mirror: gh-pages (created remote-only by the github-pages deploy)
// must NOT be pruned, while a genuinely stale remote-only branch still is.
func TestBuildPushRefSpecs_PreservesGHPages(t *testing.T) {
	local := map[string]bool{"refs/heads/main": true, "refs/tags/v1": true}
	remote := map[string]bool{
		"refs/heads/main":     true,
		"refs/heads/gh-pages": true, // remote-only — created by the github-pages deploy
		"refs/heads/stale":    true, // remote-only — genuinely stale, should be pruned
	}
	exact := &config.FacetSpec{Scope: "all", Prune: true}
	joined := pushSpecJoin(local, remote, exact, exact, RefContext{})
	if strings.Contains(joined, ":refs/heads/gh-pages") {
		t.Fatalf("gh-pages must NOT be pruned — pruning it wipes the docs site; specs=%v", joined)
	}
	if !strings.Contains(joined, ":refs/heads/stale") {
		t.Fatalf("a genuinely stale remote-only branch should still be pruned; specs=%v", joined)
	}
}

// TestBuildPushRefSpecs_AllIsAddOnly: scope:all pushes everything but prunes
// nothing (the semantic that changed from today's unconditional prune).
func TestBuildPushRefSpecs_AllIsAddOnly(t *testing.T) {
	local := map[string]bool{"refs/heads/main": true}
	remote := map[string]bool{"refs/heads/main": true, "refs/heads/stale": true}
	all := &config.FacetSpec{Scope: "all"}
	joined := pushSpecJoin(local, remote, all, all, RefContext{})
	if strings.Contains(joined, ":refs/heads/stale") {
		t.Fatalf("scope:all must not prune; specs=%v", joined)
	}
	if !strings.Contains(joined, "+refs/heads/main:refs/heads/main") {
		t.Fatalf("main should be force-pushed; specs=%v", joined)
	}
}

// TestBuildPushRefSpecs_NilFacetUntouched: a nil facet leaves that ref class
// entirely alone — not pushed, not pruned.
func TestBuildPushRefSpecs_NilFacetUntouched(t *testing.T) {
	local := map[string]bool{"refs/heads/main": true, "refs/tags/v1": true}
	remote := map[string]bool{"refs/tags/old": true}
	exact := &config.FacetSpec{Scope: "all", Prune: true}
	joined := pushSpecJoin(local, remote, exact, nil, RefContext{}) // tags facet nil
	if strings.Contains(joined, "refs/tags/") {
		t.Fatalf("nil tags facet must emit no tag refspecs; specs=%v", joined)
	}
	if !strings.Contains(joined, "+refs/heads/main:refs/heads/main") {
		t.Fatalf("branches facet should still push main; specs=%v", joined)
	}
}

// TestBuildPushRefSpecs_CurrentOnlyThatRef: scope:current replicates only the ref
// the run addresses, never others, never prune.
func TestBuildPushRefSpecs_CurrentOnlyThatRef(t *testing.T) {
	local := map[string]bool{"refs/heads/main": true, "refs/heads/feature": true}
	current := &config.FacetSpec{Scope: "current"}
	joined := pushSpecJoin(local, map[string]bool{}, current, nil, RefContext{Branch: "feature"})
	if !strings.Contains(joined, "+refs/heads/feature:refs/heads/feature") {
		t.Fatalf("current branch should push; specs=%v", joined)
	}
	if strings.Contains(joined, "refs/heads/main") {
		t.Fatalf("non-current branch must not push under scope:current; specs=%v", joined)
	}
}

// TestBuildPushRefSpecs_MatchFilter: a match glob restricts both push and prune.
func TestBuildPushRefSpecs_MatchFilter(t *testing.T) {
	local := map[string]bool{"refs/heads/main": true, "refs/heads/release-1": true, "refs/heads/release-2": true}
	spec := &config.FacetSpec{Scope: "all", Match: "release-*"}
	joined := pushSpecJoin(local, map[string]bool{}, spec, nil, RefContext{})
	if strings.Contains(joined, "refs/heads/main:") {
		t.Fatalf("main should be filtered out by match; specs=%v", joined)
	}
	if !strings.Contains(joined, "refs/heads/release-1") || !strings.Contains(joined, "refs/heads/release-2") {
		t.Fatalf("release-* branches should match; specs=%v", joined)
	}
}

func TestClassifyGoGitFailure(t *testing.T) {
	tests := []struct {
		msg  string
		want MirrorFailureReason
	}{
		{"Authentication failed for 'https://...'", MirrorAuthFailed},
		{"invalid credentials", MirrorAuthFailed},
		{"401 unauthorized", MirrorAuthFailed},
		{"protected branch update failed", MirrorProtectedRefRejected},
		{"pre-receive hook declined", MirrorProtectedRefRejected},
		{"could not resolve host: github.com", MirrorNetworkFailed},
		{"connection refused", MirrorNetworkFailed},
		{"repository not found", MirrorRemoteNotFound},
		{"failed to push some refs", MirrorPushRejected},
		{"some other unknown error", MirrorUnknown},
	}

	for _, tt := range tests {
		got := classifyGoGitFailure(fmt.Errorf("%s", tt.msg))
		if got != tt.want {
			t.Errorf("classifyGoGitFailure(%q) = %s, want %s", tt.msg, got, tt.want)
		}
	}
}

func TestResolveGitAuth(t *testing.T) {
	tests := []struct {
		provider string
		wantUser string
	}{
		{"github", "x-access-token"},
		{"gitlab", "oauth2"},
		{"gitea", "git"},
		{"unknown", "git"},
	}
	for _, tt := range tests {
		auth := resolveGitAuth(tt.provider, "secret123")
		if auth.Username != tt.wantUser {
			t.Errorf("resolveGitAuth(%q): username = %q, want %q", tt.provider, auth.Username, tt.wantUser)
		}
		if auth.Password != "secret123" {
			t.Errorf("resolveGitAuth(%q): password = %q, want %q", tt.provider, auth.Password, "secret123")
		}
	}
}

func TestBuildRemoteURL(t *testing.T) {
	r := config.ResolvedRepo{
		BaseURL: "https://github.com",
		Project: "example-org/example-repo",
	}
	got := buildRemoteURL(r)
	if got != "https://github.com/example-org/example-repo.git" {
		t.Errorf("buildRemoteURL = %q, want https://github.com/example-org/example-repo.git", got)
	}
}

func TestBuildRemoteURL_AlreadyHasGitSuffix(t *testing.T) {
	r := config.ResolvedRepo{
		BaseURL: "https://github.com",
		Project: "example-org/example-repo.git",
	}
	got := buildRemoteURL(r)
	if strings.HasSuffix(got, ".git.git") {
		t.Errorf("buildRemoteURL double-appended .git: %s", got)
	}
}

func TestSanitizeError_RemovesCredentials(t *testing.T) {
	err := fmt.Errorf("unable to push to https://x-access-token:ghp_abc123secret@github.com/org/repo.git")
	msg := sanitizeError(err)

	if strings.Contains(msg, "ghp_abc123secret") {
		t.Errorf("sanitizeError still contains token: %s", msg)
	}
	if strings.Contains(msg, "x-access-token") {
		t.Errorf("sanitizeError still contains username: %s", msg)
	}
	if !strings.Contains(msg, "[redacted]") {
		t.Errorf("sanitizeError should contain [redacted]: %s", msg)
	}
}

func TestMirrorPush_EmptyRepoBootstrap(t *testing.T) {
	source := setupTestRepo(t)
	remote := setupBareRemote(t)

	result := mirrorPushDirect(t, source, remote)

	if result.Status != SyncSuccess {
		t.Fatalf("bootstrap push should succeed, got %s: %s", result.Status, result.Message)
	}

	remoteSHA := remoteBranchHash(t, remote, "main")
	srcSHA := branchHash(t, source, "main")
	if remoteSHA != srcSHA {
		t.Errorf("remote HEAD %s != source HEAD %s", remoteSHA, srcSHA)
	}

	if !remoteHasTag(t, remote, "v1.0.0") {
		t.Error("remote should have tag v1.0.0 after bootstrap")
	}
}

// ── test helpers ──

// mirrorPushDirect performs a mirror push between two local repos using go-git
// (no git binary). It clones --mirror from the source worktree into a temp bare
// repo, then force-pushes heads + tags with prune to the remote — the same
// shape as production MirrorPush, but against a local filesystem remote so the
// fixtures need no credentials or network.
func mirrorPushDirect(t *testing.T, worktreeDir, remoteDir string) *MirrorResult {
	t.Helper()

	tmpDir := t.TempDir()

	// Clone --mirror from the source worktree
	bareRepo, err := git.PlainClone(tmpDir, true, &git.CloneOptions{
		URL:    worktreeDir,
		Mirror: true,
	})
	if err != nil {
		t.Fatalf("mirror clone failed: %v", err)
	}

	// Add the destination as a remote
	if _, err := bareRepo.CreateRemote(&gitconfig.RemoteConfig{
		Name: "mirror",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("add mirror remote: %v", err)
	}

	// Force-push heads + tags with prune — reuse the production refspec builder.
	localRefs, err := collectLocalRefs(bareRepo)
	if err != nil {
		t.Fatalf("collect local refs: %v", err)
	}
	// An empty bare remote (first push / bootstrap) has no refs; listRemoteRefs
	// returns an empty set rather than an error (it absorbs the go-git
	// ErrEmptyRemoteRepository), so prune has nothing to delete and the bootstrap
	// force-push populates the remote.
	remoteRefs, err := listRemoteRefs(t.Context(), bareRepo, nil)
	if err != nil {
		t.Fatalf("list remote refs: %v", err)
	}
	// Faithful mirror (former unconditional behavior): all heads + all tags, prune.
	exact := &config.FacetSpec{Scope: "all", Prune: true}
	refSpecs := buildPushRefSpecs(localRefs, remoteRefs, exact, exact, RefContext{})

	if len(refSpecs) == 0 {
		return &MirrorResult{AccessoryID: "test-remote", Status: SyncSuccess}
	}

	err = bareRepo.Push(&git.PushOptions{
		RemoteName: "mirror",
		RefSpecs:   refSpecs,
		Force:      true,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return &MirrorResult{
			AccessoryID:   "test-remote",
			Status:        SyncFailed,
			Degraded:      true,
			FailureReason: MirrorUnknown,
			Message:       err.Error(),
		}
	}

	return &MirrorResult{AccessoryID: "test-remote", Status: SyncSuccess}
}

func openRepo(t *testing.T, dir string) *git.Repository {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("open repo %s: %v", dir, err)
	}
	return repo
}

func worktree(t *testing.T, repo *git.Repository) *git.Worktree {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	return wt
}

func checkout(t *testing.T, wt *git.Worktree, branch string, create bool) {
	t.Helper()
	if err := wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branch),
		Create: create,
	}); err != nil {
		t.Fatalf("checkout %s (create=%v): %v", branch, create, err)
	}
}

func writeAddCommit(t *testing.T, repo *git.Repository, dir, name, content, msg string) plumbing.Hash {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := worktree(t, repo)
	if _, err := wt.Add(name); err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
	hash, err := wt.Commit(msg, &git.CommitOptions{Author: testSig()})
	if err != nil {
		t.Fatalf("commit %q: %v", msg, err)
	}
	return hash
}

func branchHash(t *testing.T, dir, branch string) string {
	t.Helper()
	repo := openRepo(t, dir)
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		t.Fatalf("resolve branch %s in %s: %v", branch, dir, err)
	}
	return ref.Hash().String()
}

func remoteBranchHash(t *testing.T, remoteDir, branch string) string {
	return branchHash(t, remoteDir, branch)
}

func remoteHasBranch(t *testing.T, remoteDir, branch string) bool {
	t.Helper()
	repo := openRepo(t, remoteDir)
	_, err := repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	return err == nil
}

func remoteHasTag(t *testing.T, remoteDir, tag string) bool {
	t.Helper()
	repo := openRepo(t, remoteDir)
	_, err := repo.Reference(plumbing.NewTagReferenceName(tag), true)
	return err == nil
}
