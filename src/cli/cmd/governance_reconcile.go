package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/governance"
	"github.com/spf13/cobra"
)

var (
	govDryRun bool
	govApply  bool   // explicit flag required to enable real commits
	govSource string // override governance source repo URL
	govRef    string // override governance source ref
	govPath   string // override governance clusters file path
)

var governanceReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile governance policy to satellite repos",
	Long: `Reads governance clusters from the policy repo, resolves presets,
generates managed configs, and commits to satellite repos.

Forge identity (provider, URL, credentials) is read from sources.primary
in .stagefreight.yml — the same config every StageFreight repo uses.

Use --dry-run to preview changes without committing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return executeGovernanceReconcile(cmd.Context(), GovernanceReconcileOpts{
			DryRun:  govDryRun,
			Apply:   govApply,
			Source:  govSource,
			Ref:     govRef,
			Path:    govPath,
			Config:  cfg,
			Verbose: verbose,
		})
	},
}

func init() {
	governanceReconcileCmd.Flags().BoolVar(&govDryRun, "dry-run", false, "Preview changes without committing")
	governanceReconcileCmd.Flags().BoolVar(&govApply, "apply", false, "Actually commit changes (required for real writes)")
	governanceReconcileCmd.Flags().StringVar(&govSource, "source", "", "Override governance source repo URL")
	governanceReconcileCmd.Flags().StringVar(&govRef, "ref", "", "Override governance source ref")
	governanceReconcileCmd.Flags().StringVar(&govPath, "path", "", "Override governance clusters file path")
	governanceCmd.AddCommand(governanceReconcileCmd)
}

// GovernanceReconcileOpts carries all inputs for governance reconciliation.
// No package globals, no cobra dependency — all execution state is explicit.
type GovernanceReconcileOpts struct {
	DryRun  bool
	Apply   bool
	Source  string // override governance source repo URL
	Ref     string // override governance source ref
	Path    string // override governance clusters file path
	Config  *config.Config
	CICtx   *ci.CIContext // nil = not in CI (source resolution skips CI layer)
	Verbose bool
}

// executeGovernanceReconcile is the core reconcile logic.
// Takes an explicit context and opts — no cobra dependency, no package globals.
func executeGovernanceReconcile(ctx context.Context, opts GovernanceReconcileOpts) error {
	// Mode validation — exactly one must be set.
	if opts.DryRun && opts.Apply {
		return fmt.Errorf("invalid options: dry-run and apply are mutually exclusive")
	}
	if !opts.DryRun && !opts.Apply {
		return fmt.Errorf("must specify either --apply or --dry-run")
	}

	// Resolve governance source.
	source, err := resolveGovernanceSourceFromOpts(opts)
	if err != nil {
		return err
	}

	// Progress chatter is VERBOSE-only: every fact below (source, ref, forge, profile
	// and repo counts) is a row in the structured view this command ends with, and
	// printing it twice — unaligned, above the box — is the noise, not the signal.
	// Under -v it stays, because a long reconcile against a slow forge is worth watching.
	vlog := func(format string, args ...any) {
		if opts.Verbose {
			fmt.Fprintf(os.Stderr, format, args...)
		}
	}

	vlog("Governance source: %s @ %s\n", source.RepoURL, source.Ref)
	vlog("Clusters path: %s\n", source.Path)

	// Phase 1: Load governance config + presets.
	vlog("\nLoading governance config...\n")
	// Retain what materialization fetches in the control repo's own cache, so every
	// source must be reachable the first time but not on every reconcile.
	if wd, werr := os.Getwd(); werr == nil {
		governance.PresetMaterializeCache = filepath.Join(wd, ".stagefreight", "preset-cache")
	}

	// One checkout per repo+ref is reused across every preset read; drop them when the
	// reconcile is done rather than leaving them for process exit.
	defer governance.ReleaseCheckouts()

	gov, presetLoader, err := governance.LoadGovernance(source)
	if err != nil {
		return fmt.Errorf("loading governance: %w", err)
	}

	vlog("  profiles: %d\n", len(gov.Profiles))
	totalRepos := 0
	for _, c := range gov.Profiles {
		n := len(c.Repos)
		totalRepos += n
		vlog("  profile %q: %d repos\n", c.ID, n)
	}

	// Phase 2: Plan distribution.
	vlog("\nPlanning distribution for %d repos...\n", totalRepos)

	_, _, sourceIdentity, parseErr := config.ParseForgeURL(source.RepoURL)
	if parseErr != nil {
		return fmt.Errorf("parsing governance source URL: %w", parseErr)
	}

	// Resolve forge from sources.primary — standard StageFreight config resolution.
	forgeProvider, forgeBaseURL, forgeCreds, err := resolveGovernanceForgeFromConfig(opts.Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ forge unresolved: %v — drift detection disabled\n", err)
	}

	var adapter *forgeAdapter
	var forgeReader governance.ForgeReader
	var forgeSummary string
	if forgeBaseURL != "" {
		factory := &forge.BasicFactory{
			ProviderName: forgeProvider,
			BaseURL:      forgeBaseURL,
			CredPrefix:   forgeCreds,
		}
		adapter = &forgeAdapter{factory: factory, ctx: ctx}
		forgeReader = adapter
		forgeSummary = fmt.Sprintf("%s @ %s (cred: %s_*)", forgeProvider, forgeBaseURL, forgeCreds)
	}

	presetSource := governance.PresetSourceInfo{
		Provider:  forgeProvider,
		ForgeURL:  forgeBaseURL,
		ProjectID: sourceIdentity,
	}

	plans, err := governance.PlanDistribution(
		gov, presetLoader, governance.FetchFile,
		forgeReader,
		presetSource, sourceIdentity,
	)
	if err != nil {
		return fmt.Errorf("planning distribution: %w", err)
	}

	// Phase 5: Render plan view.
	planByRepo := make(map[string]governance.DistributionPlan, len(plans))
	for _, p := range plans {
		planByRepo[p.Repo] = p
	}

	if opts.DryRun {
		governance.RenderPlanView(os.Stdout, governance.PlanViewData{
			Config: governance.PlanViewConfig{
				Mode:    "dry-run",
				Source:  sourceIdentity,
				Ref:     source.Ref,
				Forge:   forgeSummary,
				Verbose: opts.Verbose,
			},
			Profiles: gov.Profiles,
			Plans:    planByRepo,
		})
		return nil
	}

	// Phase 6: Commit to satellite repos (Apply mode — validated at entry).
	if adapter == nil {
		return fmt.Errorf("sources.primary required for --apply mode (forge identity not resolved)")
	}

	// Populate per-repo credential overrides from cluster targets.
	credOverrides := make(map[string]string)
	for _, p := range plans {
		if p.Credentials != "" {
			credOverrides[p.Repo] = p.Credentials
		}
	}
	adapter.credOverrides = credOverrides

	vlog("\nCommitting to satellite repos...\n")
	// Host-qualified: "PrPlanIT/MaintenancePolicy" alone cannot tell a reader whether
	// the policy came from the internal GitLab or from github.com.
	sourceLabel := sourceIdentity
	if u, uerr := url.Parse(source.RepoURL); uerr == nil && u.Host != "" {
		sourceLabel = u.Host + "/" + sourceIdentity
	}
	results, err := governance.CommitDistribution(plans, adapter, sourceLabel, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nReconcile completed with errors\n")
	}

	resultByRepo := make(map[string]governance.CommitResult, len(results))
	for _, r := range results {
		resultByRepo[r.Repo] = r
	}

	governance.RenderPlanView(os.Stdout, governance.PlanViewData{
		Config: governance.PlanViewConfig{
			Mode:    "apply",
			Source:  sourceIdentity,
			Ref:     source.Ref,
			Forge:   forgeSummary,
			Verbose: opts.Verbose,
		},
		Profiles: gov.Profiles,
		Plans:    planByRepo,
		Results:  resultByRepo,
	})

	return err
}

// resolveGovernanceSourceFromOpts determines the governance source via three
// explicit resolution layers, applied in priority order:
//  1. Explicit overrides (CLI flags or caller-provided opts)
//  2. CI context inference (when running in a governance repo in CI)
//  3. Config fallback (sources.primary.url)
func resolveGovernanceSourceFromOpts(opts GovernanceReconcileOpts) (governance.GovernanceSource, error) {
	source := governance.GovernanceSource{}

	// Layer 1: Explicit overrides — highest priority.
	source.RepoURL = opts.Source
	source.Ref = opts.Ref
	source.Path = opts.Path

	// Layer 2: CI context — infer from caller-provided CI state.
	if opts.CICtx != nil && opts.CICtx.IsCI() && opts.Config != nil && opts.Config.Mode().Name == config.ModeGovernance {
		if source.RepoURL == "" {
			source.RepoURL = opts.CICtx.RepoURL
		}
		if source.Ref == "" {
			source.Ref = opts.CICtx.SHA
		}
		if opts.CICtx.Workspace != "" {
			source.LocalPath = opts.CICtx.Workspace
		}
	}

	// Layer 3: Config fallback — sources.primary.url from .stagefreight.yml.
	if source.RepoURL == "" && opts.Config != nil && config.PrimaryURL(opts.Config) != "" {
		source.RepoURL = config.PrimaryURL(opts.Config)
	}

	// Default path.
	if source.Path == "" {
		source.Path = ".stagefreight.yml"
	}

	// Validate — both are required after all layers applied.
	if source.RepoURL == "" {
		return source, fmt.Errorf("governance source required: set sources.primary.url in .stagefreight.yml")
	}
	// No ref is the DEFAULT, not an omission: an unpinned source tracks its default
	// branch, so satellites see policy changes on their next run. Pinning is the
	// operator's opt-out from that, expressed by naming a ref.

	return source, nil
}

// resolveGovernanceForgeFromConfig reads forge identity from the standard sources.primary config.
func resolveGovernanceForgeFromConfig(appCfg *config.Config) (provider, baseURL, credPrefix string, err error) {
	if appCfg == nil || config.PrimaryURL(appCfg) == "" {
		return "", "", "", fmt.Errorf("sources.primary.url not configured")
	}

	provider, baseURL, _, err = config.ParseForgeURL(config.PrimaryURL(appCfg))
	if err != nil {
		return "", "", "", fmt.Errorf("parsing sources.primary.url: %w", err)
	}

	credPrefix = strings.ToUpper(provider)
	return provider, baseURL, credPrefix, nil
}

// forgeAdapter wraps forge.Factory to satisfy both governance.ForgeReader and governance.ForgeClient.
// Supports per-repo credential overrides via credOverrides map.
type forgeAdapter struct {
	factory       forge.Factory
	ctx           context.Context
	credOverrides map[string]string // repo → credential prefix override
}

// forgeForRepo returns a forge client for the given repo, respecting credential overrides.
func (a *forgeAdapter) forgeForRepo(repo string) (forge.Forge, error) {
	if cred, ok := a.credOverrides[repo]; ok && cred != "" {
		// Use overridden credentials — create factory with different prefix.
		baseFactory := a.factory.(*forge.BasicFactory)
		overrideFactory := &forge.BasicFactory{
			ProviderName: baseFactory.ProviderName,
			BaseURL:      baseFactory.BaseURL,
			CredPrefix:   cred,
		}
		return overrideFactory.ForRepo(a.ctx, repo)
	}
	return a.factory.ForRepo(a.ctx, repo)
}

func (a *forgeAdapter) GetFileContent(repo, path, ref string) ([]byte, error) {
	f, err := a.forgeForRepo(repo)
	if err != nil {
		return nil, fmt.Errorf("creating forge for %s: %w", repo, err)
	}
	return f.GetFileContent(a.ctx, path, ref)
}

func (a *forgeAdapter) DefaultBranch(repo string) (string, error) {
	f, err := a.forgeForRepo(repo)
	if err != nil {
		return "", fmt.Errorf("creating forge for %s: %w", repo, err)
	}
	return f.DefaultBranch(a.ctx)
}

func (a *forgeAdapter) CommitFiles(repo, branch, message string, files []governance.FileCommit) (string, error) {
	f, err := a.forgeForRepo(repo)
	if err != nil {
		return "", fmt.Errorf("creating forge for %s: %w", repo, err)
	}

	forgeFiles := make([]forge.FileAction, 0, len(files))
	for _, fc := range files {
		forgeFiles = append(forgeFiles, forge.FileAction{
			Path:    fc.Path,
			Content: fc.Content,
		})
	}

	result, err := f.CommitFiles(a.ctx, forge.CommitFilesOptions{
		Branch:  branch,
		Message: message,
		Files:   forgeFiles,
	})
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	return result.SHA, nil
}
