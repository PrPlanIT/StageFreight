package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// goCommit commits the currently staged worktree with a fixed test signature
// and returns the new commit hash. go-git is used directly so the release
// fixtures carry no dependency on the git CLI (enforced by TestNoGitShellOuts).
func goCommit(t *testing.T, wt *git.Worktree, msg string) plumbing.Hash {
	t.Helper()
	h, err := wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("commit %q: %v", msg, err)
	}
	return h
}

// initMainRepo initializes a non-bare repo with HEAD on main and returns the
// repo plus its worktree. go-git's PlainInit defaults HEAD to master; the
// release tests assume main, so set it explicitly.
func initMainRepo(t *testing.T, dir string) (*git.Repository, *git.Worktree) {
	t.Helper()
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
	return repo, wt
}

// setupTestRepo creates a temporary git repo with a linear commit history
// and the specified tags. Returns the repo directory.
// Each commit is a trivial file change so tags land on distinct commits.
func setupTestRepo(t *testing.T, commits int, tags map[int][]string) string {
	t.Helper()

	dir := t.TempDir()
	repo, wt := initMainRepo(t, dir)

	for i := 1; i <= commits; i++ {
		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte{byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("file.txt"); err != nil {
			t.Fatalf("add file.txt: %v", err)
		}
		hash := goCommit(t, wt, "commit "+string(rune('0'+i)))

		// tags[i] is nil when absent — ranging over nil is a no-op.
		for _, tag := range tags[i] {
			if _, err := repo.CreateTag(tag, hash, nil); err != nil {
				t.Fatalf("tag %s: %v", tag, err)
			}
		}
	}

	return dir
}

func TestPreviousReleaseTag_SkipsLatest(t *testing.T) {
	// Commit history: 1(v0.0.2) -> 2(latest) -> 3(v0.1.0)
	repo := setupTestRepo(t, 3, map[int][]string{
		1: {"v0.0.2"},
		2: {"latest"},
		3: {"v0.1.0"},
	})

	got, err := PreviousReleaseTag(repo, "v0.1.0", []string{`^v?\d+\.\d+\.\d+$`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.0.2" {
		t.Errorf("got %q, want %q", got, "v0.0.2")
	}
}

func TestPreviousReleaseTag_SkipsSameVersionAlias(t *testing.T) {
	// Commit history: 1(v0.0.2) -> 2(0.1.0) -> 3(v0.1.0)
	// 0.1.0 is a stale bare-version alias from a failed release attempt.
	repo := setupTestRepo(t, 3, map[int][]string{
		1: {"v0.0.2"},
		2: {"0.1.0"},
		3: {"v0.1.0"},
	})

	got, err := PreviousReleaseTag(repo, "v0.1.0", []string{`^v?\d+\.\d+\.\d+$`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.0.2" {
		t.Errorf("got %q, want %q", got, "v0.0.2")
	}
}

func TestPreviousReleaseTag_SkipsSameCommitAlias(t *testing.T) {
	// v0.1.0 and 0.1.0 on the SAME commit (rolling alias created during release).
	// Must still find v0.0.2.
	repo := setupTestRepo(t, 2, map[int][]string{
		1: {"v0.0.2"},
		2: {"v0.1.0", "0.1.0", "latest"},
	})

	got, err := PreviousReleaseTag(repo, "v0.1.0", []string{`^v?\d+\.\d+\.\d+$`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.0.2" {
		t.Errorf("got %q, want %q", got, "v0.0.2")
	}
}

func TestPreviousReleaseTag_DefaultPatternFallback(t *testing.T) {
	// No patterns provided — should fall back to default semver matcher.
	repo := setupTestRepo(t, 3, map[int][]string{
		1: {"v0.0.2"},
		2: {"latest"},
		3: {"v0.1.0"},
	})

	got, err := PreviousReleaseTag(repo, "v0.1.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.0.2" {
		t.Errorf("got %q, want %q", got, "v0.0.2")
	}
}

func TestPreviousReleaseTag_PatternExcludesBareVersion(t *testing.T) {
	// Policy only matches v-prefixed tags. Bare 0.1.0 must be ignored
	// even though it's a valid ancestor.
	repo := setupTestRepo(t, 3, map[int][]string{
		1: {"v0.0.2"},
		2: {"0.1.0"},
		3: {"v0.2.0"},
	})

	got, err := PreviousReleaseTag(repo, "v0.2.0", []string{`^v\d+\.\d+\.\d+$`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.0.2" {
		t.Errorf("got %q, want %q", got, "v0.0.2")
	}
}

func TestPreviousReleaseTag_NonAncestorSkipped(t *testing.T) {
	// Create a branch with a higher version tag that's not an ancestor of main.
	dir := t.TempDir()
	repo, wt := initMainRepo(t, dir)

	writeAdd := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("f.txt"); err != nil {
			t.Fatalf("add f.txt: %v", err)
		}
	}
	tag := func(name string, h plumbing.Hash) {
		t.Helper()
		if _, err := repo.CreateTag(name, h, nil); err != nil {
			t.Fatalf("tag %s: %v", name, err)
		}
	}

	// Commit 1: base on main
	writeAdd("1")
	base := goCommit(t, wt, "base")
	tag("v0.0.1", base)

	// Branch off to 'other' with a higher version tag not on main's history
	if err := wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("other"),
		Create: true,
	}); err != nil {
		t.Fatalf("checkout other: %v", err)
	}
	writeAdd("other")
	other := goCommit(t, wt, "other branch")
	tag("v0.9.0", other) // higher version, not on main's history

	// Back to main
	if err := wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("main"),
	}); err != nil {
		t.Fatalf("checkout main: %v", err)
	}
	writeAdd("2")
	mainCommit := goCommit(t, wt, "main commit")
	tag("v0.1.0", mainCommit)

	// v0.9.0 exists but is NOT an ancestor of v0.1.0
	got, err := PreviousReleaseTag(dir, "v0.1.0", []string{`^v?\d+\.\d+\.\d+$`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.0.1" {
		t.Errorf("got %q, want %q", got, "v0.0.1")
	}
}

func TestPreviousReleaseTag_NoPreviousTag(t *testing.T) {
	// Only the current tag exists. Should return empty, not error.
	repo := setupTestRepo(t, 1, map[int][]string{
		1: {"v0.1.0"},
	})

	got, err := PreviousReleaseTag(repo, "v0.1.0", []string{`^v?\d+\.\d+\.\d+$`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestPreviousReleaseTag_BareCurrentRef(t *testing.T) {
	// currentRef passed as bare version "0.1.0" — same-version exclusion
	// must still work against v0.1.0 tags in history.
	repo := setupTestRepo(t, 3, map[int][]string{
		1: {"v0.0.2"},
		2: {"v0.1.0"},
		3: {"0.1.0"},
	})

	got, err := PreviousReleaseTag(repo, "0.1.0", []string{`^v?\d+\.\d+\.\d+$`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.0.2" {
		t.Errorf("got %q, want %q", got, "v0.0.2")
	}
}

func TestPreviousReleaseTag_PrereleaseIncluded(t *testing.T) {
	// Prerelease tags matching the prerelease policy should be eligible.
	repo := setupTestRepo(t, 3, map[int][]string{
		1: {"v0.0.2"},
		2: {"v0.1.0-rc1"},
		3: {"v0.1.0"},
	})

	patterns := []string{
		`^v?\d+\.\d+\.\d+$`,
		`^v?\d+\.\d+\.\d+-.+`,
	}

	got, err := PreviousReleaseTag(repo, "v0.1.0", patterns)
	if err != nil {
		t.Fatal(err)
	}
	// v0.1.0-rc1 is a different normalized version (0.1.0-rc1 != 0.1.0)
	// and is closer than v0.0.2, so it should be found first.
	if got != "v0.1.0-rc1" {
		t.Errorf("got %q, want %q", got, "v0.1.0-rc1")
	}
}

func TestNormalizeReleaseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v0.1.0", "0.1.0"},
		{"0.1.0", "0.1.0"},
		{"refs/tags/v0.1.0", "0.1.0"},
		{"refs/tags/0.1.0", "0.1.0"},
		{"v1.2.3-rc1", "1.2.3-rc1"},
		{"latest", "latest"},
	}
	for _, tt := range tests {
		got := normalizeReleaseVersion(tt.input)
		if got != tt.want {
			t.Errorf("normalizeReleaseVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCompileReleaseTagMatchers_InvalidPattern(t *testing.T) {
	_, err := compileReleaseTagMatchers([]string{`[invalid`})
	if err == nil {
		t.Error("expected error for invalid regex pattern, got nil")
	}
}

func TestCompileReleaseTagMatchers_EmptyFallback(t *testing.T) {
	matchers, err := compileReleaseTagMatchers(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(matchers) != 1 {
		t.Fatalf("expected 1 default matcher, got %d", len(matchers))
	}
	if !matchers[0].MatchString("v0.1.0") {
		t.Error("default matcher should match v0.1.0")
	}
	if !matchers[0].MatchString("0.1.0") {
		t.Error("default matcher should match 0.1.0")
	}
	if matchers[0].MatchString("latest") {
		t.Error("default matcher should NOT match latest")
	}
}

func TestRenderVerification_DisclosesTierAndRecipe(t *testing.T) {
	out := renderVerification(&Verification{
		TierLabel:        "Tier-0 (persistent software key)",
		Fingerprint:      "sha256:abc123",
		AnchorAsset:      "cosign.pub",
		ChecksumSig:      "SHA256SUMS.sig",
		Transparency:     false,
		NonExportable:    false,
		PhysicalPresence: false,
		Continuity:       true,
	})
	for _, want := range []string{
		"## Verification",
		"Tier-0 (persistent software key)",
		"sha256:abc123",
		"Signer continuity | stable",
		"Non-exportable key | no",
		"cosign verify-blob",
		"--key cosign.pub",
		"--signature SHA256SUMS.sig",
		"--insecure-ignore-tlog=true", // no transparency → ignore tlog
	} {
		if !strings.Contains(out, want) {
			t.Errorf("verification section missing %q:\n%s", want, out)
		}
	}
}

// Provenance attestations must be disclosed in their OWN section, never folded
// into the signature layers — "signed" and "provenance attested by tier X" are
// distinct trust statements, and the verify recipe differs (verify-attestation).
func TestRenderVerification_DisclosesProvenanceAttestationsSeparately(t *testing.T) {
	out := renderVerification(&Verification{
		TierLabel:    "Tier-0 (persistent software key)",
		Transparency: false,
		ProvenanceAttestations: []string{
			"slsaprovenance · key (Tier-0 (persistent software key)) · sha256:cafe",
		},
	})
	for _, want := range []string{
		"Build provenance is cryptographically attested",
		"slsaprovenance · key",
		"cosign verify-attestation --type slsaprovenance",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("provenance attestation disclosure missing %q:\n%s", want, out)
		}
	}
}

// A non-anchor OIDC release must still render a verify section — a keyless recipe
// (certificate-identity / oidc-issuer), the trust domain, and NOT the --key
// cosign.pub anchor recipe (which only applies when a continuity anchor exists).
func TestRenderVerification_OIDCNoAnchorEmitsKeylessRecipe(t *testing.T) {
	out := renderVerification(&Verification{
		TierLabel: "keyless (OIDC identity)", TrustClass: "oidc", TrustDomain: "internal",
		Transparency: true, SignerRef: "https://id.internal/subj",
	})
	for _, want := range []string{
		"## Verification", "Trust domain | internal", "keyless-signed",
		"cosign verify-blob", "--certificate-oidc-issuer",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("keyless verification missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--key cosign.pub") {
		t.Errorf("no anchor present → must not emit the --key cosign.pub recipe:\n%s", out)
	}
}

// A non-anchor kms/hardware release renders a signer-pointer recipe.
func TestRenderVerification_NoAnchorSignerPointerRecipe(t *testing.T) {
	out := renderVerification(&Verification{
		TierLabel: "KMS / managed key", TrustClass: "kms", NonExportable: true,
		SignerRef: "release-signing-key",
	})
	for _, want := range []string{"signed by `release-signing-key`", "trust class **kms**", "SECURITY.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("signer-pointer recipe missing %q:\n%s", want, out)
		}
	}
}

func TestRenderVerification_TransparencyOmitsIgnoreTlog(t *testing.T) {
	out := renderVerification(&Verification{
		TierLabel: "oidc keyless", Fingerprint: "", AnchorAsset: "cosign.pub",
		ChecksumSig: "SHA256SUMS.sig", Transparency: true,
	})
	if strings.Contains(out, "--insecure-ignore-tlog") {
		t.Errorf("transparency-backed verify must not skip the tlog:\n%s", out)
	}
	if !strings.Contains(out, "Transparency log | yes") {
		t.Errorf("transparency should be disclosed yes:\n%s", out)
	}
}
