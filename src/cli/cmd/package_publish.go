package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/retention"
)

// packagePublishResult captures what one generic-package target published, for
// reporting (the Distribution box, B6) and cistate.
type packagePublishResult struct {
	Target           string
	PackageName      string
	ImmutableVersion string
	ImmutableSkipped bool     // immutable version already existed — not republished
	Aliases          []string // rolling versions refreshed
	Files            []string // file names published
	PullURLs         []string // example pull URLs (immutable preferred)
	Pruned           []string // immutable versions pruned by retention
}

// packagePublishRunner publishes kind: generic-package targets to a forge generic
// package registry during the publish phase, alongside releaseRunner. It is a
// no-op (no card) when no generic-package target matches the current event, so
// release and package distribution coexist cleanly.
func packagePublishRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, opts ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)

	targets := pipeline.CollectTargetsByKind(appCfg, "generic-package")
	if len(targets) == 0 {
		return nil
	}
	var active []config.TargetConfig
	for _, t := range targets {
		if config.TargetMatchesEnv(t, appCfg) {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		return nil
	}

	// Mutation safety: never publish from a superseded pipeline.
	if !ci.IsBranchHeadFresh(ciCtx) {
		fmt.Fprintf(os.Stderr, "  package: skipped — pipeline SHA is not branch HEAD\n")
		return nil
	}

	// Shared archive resolution — same source of truth as kind: release.
	assets, err := artifact.ResolveSuccessfulArchiveAssets(rootDir)
	if err != nil {
		if errors.Is(err, artifact.ErrOutputsManifestNotFound) || errors.Is(err, artifact.ErrResultsManifestNotFound) {
			return nil // nothing built to publish
		}
		return fmt.Errorf("package subsystem: resolving archives: %w", err)
	}
	if len(assets) == 0 {
		return nil
	}

	vi, err := build.DetectVersion(rootDir, appCfg)
	if err != nil {
		return fmt.Errorf("package subsystem: detecting version: %w", err)
	}

	var results []packagePublishResult
	for _, t := range active {
		repo := config.FindRepoByID(appCfg.Repos, t.Repo)
		if repo == nil {
			return fmt.Errorf("package subsystem: target %s: repo %q not found", t.ID, t.Repo)
		}
		resolved, rerr := config.ResolveRepo(*repo, appCfg.Forges, appCfg.Vars)
		if rerr != nil {
			return fmt.Errorf("package subsystem: target %s: %w", t.ID, rerr)
		}
		fc, ferr := forge.NewFromAccessory(resolved.Provider, resolved.BaseURL, resolved.Project, resolved.Credentials)
		if ferr != nil {
			return fmt.Errorf("package subsystem: target %s: %w", t.ID, ferr)
		}

		packageName := t.Package
		if packageName == "" {
			packageName = path.Base(resolved.Project)
		}

		immutable := gitver.ResolveTags([]string{t.Version}, vi)
		if len(immutable) == 0 || immutable[0] == "" {
			return fmt.Errorf("package subsystem: target %s: version %q resolved empty", t.ID, t.Version)
		}
		aliases := gitver.ResolveTags(t.Aliases, vi)

		res, perr := publishPackageTarget(ctx, fc, t.ID, packageName, immutable[0], aliases, assets)
		if perr != nil {
			return fmt.Errorf("package subsystem: target %s: %w", t.ID, perr)
		}

		// Retention: prune old immutable versions, protecting rolling aliases.
		if t.Retention != nil && t.Retention.Active() {
			pruneRes, prerr := prunePackageTarget(ctx, fc, packageName, t.Version, t.Aliases, *t.Retention)
			if prerr != nil {
				return fmt.Errorf("package subsystem: target %s: retention: %w", t.ID, prerr)
			}
			res.Pruned = pruneRes.Deleted
		}

		results = append(results, *res)
	}

	// Minimal summary (the boxed Distribution (package) card lands in B6).
	for _, r := range results {
		verb := "published"
		if r.ImmutableSkipped {
			verb = "refreshed (immutable exists)"
		}
		pruned := ""
		if len(r.Pruned) > 0 {
			pruned = fmt.Sprintf(", pruned %d", len(r.Pruned))
		}
		fmt.Fprintf(os.Stdout, "  package: %s %s %s%s → %d file(s)%s\n",
			verb, r.PackageName, r.ImmutableVersion, aliasSuffix(r.Aliases), len(assets), pruned)
	}

	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		st.RecordSubsystem(cistate.SubsystemState{
			Name:         "package",
			Attempted:    true,
			AllowFailure: true,
			Outcome:      "published",
		})
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}

	return nil
}

// publishPackageTarget publishes the resolved archives to one generic package.
// Immutable version: published once — skipped entirely if it already exists
// (never overwritten). Alias versions: delete-then-publish (rolling overwrite).
// Takes a forge.Forge so it is unit-testable with a fake forge.
func publishPackageTarget(ctx context.Context, fc forge.Forge, targetID, packageName, immutableVersion string, aliasVersions []string, assets []artifact.ResolvedArchiveAsset) (*packagePublishResult, error) {
	res := &packagePublishResult{
		Target:           targetID,
		PackageName:      packageName,
		ImmutableVersion: immutableVersion,
		Aliases:          aliasVersions,
	}

	// Immutable version: publish once. If it already exists, skip all its files.
	existing, err := fc.ListPackageVersions(ctx, packageName)
	if err != nil {
		return nil, fmt.Errorf("listing package versions: %w", err)
	}
	for _, v := range existing {
		if v.Version == immutableVersion {
			res.ImmutableSkipped = true
			break
		}
	}
	if !res.ImmutableSkipped {
		for _, a := range assets {
			pub, perr := fc.PublishPackageFile(ctx, forge.PublishPackageOptions{
				PackageName: packageName,
				Version:     immutableVersion,
				FileName:    filepath.Base(a.Path),
				FilePath:    a.Path,
			})
			if perr != nil {
				return nil, fmt.Errorf("publishing %s@%s: %w", filepath.Base(a.Path), immutableVersion, perr)
			}
			res.Files = append(res.Files, pub.FileName)
			res.PullURLs = append(res.PullURLs, pub.PullURL)
		}
	}

	// Alias (rolling) versions: delete-then-publish to overwrite in place.
	for _, alias := range aliasVersions {
		if derr := fc.DeletePackageVersion(ctx, packageName, alias); derr != nil {
			return nil, fmt.Errorf("refreshing alias %s: deleting old: %w", alias, derr)
		}
		for _, a := range assets {
			if _, perr := fc.PublishPackageFile(ctx, forge.PublishPackageOptions{
				PackageName: packageName,
				Version:     alias,
				FileName:    filepath.Base(a.Path),
				FilePath:    a.Path,
			}); perr != nil {
				return nil, fmt.Errorf("publishing %s@%s: %w", filepath.Base(a.Path), alias, perr)
			}
		}
	}

	return res, nil
}

// packageStore adapts a forge's generic package registry to the retention.Store
// interface. Items are keyed by version string; deletion goes through the forge,
// which resolves the version to its stable package id internally.
type packageStore struct {
	forge       forge.Forge
	packageName string
}

func (s *packageStore) List(ctx context.Context) ([]retention.Item, error) {
	versions, err := s.forge.ListPackageVersions(ctx, s.packageName)
	if err != nil {
		return nil, err
	}
	items := make([]retention.Item, 0, len(versions))
	for _, v := range versions {
		items = append(items, retention.Item{Name: v.Version, CreatedAt: v.CreatedAt})
	}
	return items, nil
}

func (s *packageStore) Delete(ctx context.Context, name string) error {
	return s.forge.DeletePackageVersion(ctx, s.packageName, name)
}

// prunePackageTarget applies retention to one generic package. The candidate set
// is the IMMUTABLE version family derived from the version template (e.g.
// "dev-{sha:8}" → "^dev-.+$") — never the aliases and never a concrete resolved
// version. Rolling aliases are added to the policy's protect set so they are
// never pruned (they don't match the dev family either, but protect makes the
// guarantee explicit and survives template changes).
func prunePackageTarget(ctx context.Context, fc forge.Forge, packageName, versionTemplate string, aliasTemplates []string, policy config.RetentionPolicy) (*retention.Result, error) {
	patterns := retention.TemplatesToPatterns([]string{versionTemplate})
	effective := policy
	effective.Protect = append(append([]string{}, policy.Protect...), aliasTemplates...)
	store := &packageStore{forge: fc, packageName: packageName}
	return retention.Apply(ctx, store, patterns, effective)
}

// aliasSuffix renders " (+ a, b)" for a non-empty alias list, else "".
func aliasSuffix(aliases []string) string {
	if len(aliases) == 0 {
		return ""
	}
	s := " (+ "
	for i, a := range aliases {
		if i > 0 {
			s += ", "
		}
		s += a
	}
	return s + ")"
}
