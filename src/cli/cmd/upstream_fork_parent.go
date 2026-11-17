package cmd

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/upstream"
)

// forkParentClient opens the forge client for a repos: entry; a variable so tests can stand
// a forge in.
var forkParentClient = func(appCfg *config.Config, repoID string) (forge.Forge, error) {
	rc := config.FindRepoByID(appCfg.Repos, repoID)
	if rc == nil {
		return nil, fmt.Errorf("%q is not a repos: entry", repoID)
	}
	r, err := config.ResolveRepo(*rc, appCfg.Forges, appCfg.Vars)
	if err != nil {
		return nil, err
	}
	return forge.NewFromAccessory(r.Provider, r.BaseURL, r.Project, r.Credentials)
}

// upstreamSources is upstream.Sources with every fork_parent source resolved first.
func upstreamSources(ctx context.Context, appCfg *config.Config, rootDir string) ([]upstream.Source, error) {
	resolveForkParents(ctx, appCfg)
	return upstream.Sources(appCfg.Upstream, rootDir)
}

// resolveForkParents asks each fork_parent source's forge what its repository was forked
// from, once per run. A lookup that fails is kept on the source, which then reports
// unreachable rather than being read.
func resolveForkParents(ctx context.Context, appCfg *config.Config) {
	for i := range appCfg.Upstream.Sources {
		s := &appCfg.Upstream.Sources[i]
		if s.ForkParent == "" || s.Git != "" || s.ForkParentGit != "" || s.ForkParentErr != "" {
			continue
		}
		url, err := forkParent(ctx, appCfg, s.ForkParent)
		if err != nil {
			s.ForkParentErr = "fork_parent " + s.ForkParent + ": " + err.Error()
			continue
		}
		s.ForkParentGit = url
	}
}

func forkParent(ctx context.Context, appCfg *config.Config, repoID string) (string, error) {
	fc, err := forkParentClient(appCfg, repoID)
	if err != nil {
		return "", err
	}
	fr, ok := fc.(forge.ForkParentReader)
	if !ok {
		return "", fmt.Errorf("%s cannot report what a repository was forked from", fc.Provider())
	}
	url, err := fr.ForkParent(ctx)
	if err != nil {
		return "", fmt.Errorf("reading the repository: %w", err)
	}
	if url == "" {
		return "", fmt.Errorf("the repository is not a fork on %s", fc.Provider())
	}
	return url, nil
}
