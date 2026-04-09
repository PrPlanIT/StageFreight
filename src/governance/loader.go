package governance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

// LoadGovernance loads governance config and returns a preset loader.
// When source.LocalPath is set (CI mode), uses the local checkout directly.
// Otherwise, fetches the policy repo at the pinned ref.
// Ref must be pinned (tag or commit SHA) unless AllowFloating is true.
func LoadGovernance(source GovernanceSource) (*GovernanceConfig, PresetLoader, error) {
	var checkoutDir string

	if source.LocalPath != "" {
		// CI mode — repo is already checked out at the correct ref.
		checkoutDir = source.LocalPath
	} else {
		if err := ValidateRef(source.Ref, source.AllowFloating); err != nil {
			return nil, nil, fmt.Errorf("governance source: %w", err)
		}

		var err error
		checkoutDir, err = fetchRepo(source.RepoURL, source.Ref)
		if err != nil {
			return nil, nil, fmt.Errorf("fetching policy repo: %w", err)
		}
	}

	// Parse governance config.
	configPath := filepath.Join(checkoutDir, source.Path)
	gov, err := parseClusters(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing governance config: %w", err)
	}

	// Return a loader rooted in the checkout dir.
	loader := &filePresetLoader{root: checkoutDir}

	return gov, loader, nil
}

// ValidateRef checks pinning rules.
// Pinned tag or commit SHA: always allowed.
// Branch ref: only if allowFloating is true.
// Empty: hard error.
func ValidateRef(ref string, allowFloating bool) error {
	if ref == "" {
		return fmt.Errorf("ref is required (pinned tag or commit SHA)")
	}

	// SHA pattern: 7-40 hex chars.
	if isSHA.MatchString(ref) {
		return nil
	}

	// Tag pattern: starts with v and has dots, or is a semver-ish string.
	if isTag.MatchString(ref) {
		return nil
	}

	// Anything else is treated as a branch.
	if !allowFloating {
		return fmt.Errorf("ref %q looks like a branch; pinned tag or commit SHA required (set allow_floating: true to override)", ref)
	}

	return nil
}

var (
	isSHA = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	isTag = regexp.MustCompile(`^v?\d+\.\d+`)
)

// fetchRepo clones the policy repo at the given ref into a temp directory.
// Returns the checkout path. Caller should NOT clean up — immutable for the run.
// Tries the ref as a tag reference, then as a branch reference. If both fail
// (e.g. ref is a commit SHA), falls back to fetchBySHA.
func fetchRepo(repoURL, ref string) (string, error) {
	auth, err := resolveGovernanceAuth(repoURL)
	if err != nil {
		return "", fmt.Errorf("resolving auth for %s: %w", repoURL, err)
	}

	for _, refName := range []plumbing.ReferenceName{
		plumbing.NewTagReferenceName(ref),
		plumbing.NewBranchReferenceName(ref),
	} {
		tmpDir, err := os.MkdirTemp("", "sf-governance-*")
		if err != nil {
			return "", fmt.Errorf("creating temp dir: %w", err)
		}
		_, cloneErr := git.PlainClone(tmpDir, false, &git.CloneOptions{
			URL:           repoURL,
			Auth:          auth,
			Depth:         1,
			SingleBranch:  true,
			ReferenceName: refName,
		})
		if cloneErr == nil {
			return tmpDir, nil
		}
		os.RemoveAll(tmpDir)
	}

	return fetchBySHA(repoURL, ref)
}

// fetchBySHA handles commit SHA refs that can't be fetched via --branch.
// Performs a full clone then checks out the specific commit.
func fetchBySHA(repoURL, sha string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "sf-governance-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}

	auth, err := resolveGovernanceAuth(repoURL)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("resolving auth for %s: %w", repoURL, err)
	}

	repo, err := git.PlainClone(tmpDir, false, &git.CloneOptions{
		URL:  repoURL,
		Auth: auth,
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("cloning %s for SHA %s: %w", repoURL, sha, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("worktree: %w", err)
	}

	if err := wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(sha)}); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("checking out %s: %w", sha, err)
	}

	return tmpDir, nil
}

// resolveGovernanceAuth returns the appropriate go-git auth method for repoURL.
// SSH URLs use gitstate.ResolveAuth; HTTPS repos are currently public (nil auth).
func resolveGovernanceAuth(repoURL string) (transport.AuthMethod, error) {
	if gitstate.IsSSHURL(repoURL) {
		return gitstate.ResolveAuth(repoURL)
	}
	return nil, nil
}

// parseClusters reads and parses the governance clusters file.
func parseClusters(path string) (*GovernanceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	// The file has a top-level "governance" key.
	var wrapper struct {
		Governance GovernanceConfig `yaml:"governance"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return &wrapper.Governance, nil
}

// FetchFile fetches a single file from a git repo at a specific ref.
// Used as the AssetFetcher for governance distribution.
func FetchFile(repoURL, ref, path string) ([]byte, error) {
	if ref == "" {
		ref = "HEAD"
	}

	checkoutDir, err := fetchRepo(repoURL, ref)
	if err != nil {
		checkoutDir, err = fetchBySHA(repoURL, ref)
		if err != nil {
			return nil, fmt.Errorf("fetching %s@%s: %w", repoURL, ref, err)
		}
	}
	defer os.RemoveAll(checkoutDir)

	filePath := filepath.Join(checkoutDir, path)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s from %s@%s: %w", path, repoURL, ref, err)
	}

	return data, nil
}

// filePresetLoader loads preset files from a local directory.
type filePresetLoader struct {
	root string
}

func (l *filePresetLoader) Load(path string) ([]byte, error) {
	fullPath := filepath.Join(l.root, path)

	// Security: prevent path traversal.
	absRoot, _ := filepath.Abs(l.root)
	absPath, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absPath, absRoot) {
		return nil, fmt.Errorf("preset path %q escapes root directory", path)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("loading preset %q: %w", path, err)
	}

	return data, nil
}
