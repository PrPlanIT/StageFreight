package facts

import "github.com/PrPlanIT/StageFreight/src/gitver"

// gitverLeaf wraps gitver's leaf template pass as a Resolver. It owns the version,
// commit, project, var, env, date, and CI fact families and resolves them in gitver's
// own internal (longest-token-first) order, unchanged — this is a thin adapter, not a
// reimplementation. Config-sourced inputs reach gitver only through the Context
// (Description via ResolveOptions), so this adapter adds no gitver→config edge.
type gitverLeaf struct{}

// GitverLeaf returns the resolver for gitver's leaf template families.
func GitverLeaf() Resolver { return gitverLeaf{} }

func (gitverLeaf) Name() string { return "gitver-leaf" }

// Provides lists gitver's leaf families for ordering purposes. gitver resolves every
// token it owns internally regardless; this list only governs where the leaf pass
// sits relative to other resolvers (e.g. run-identity facts resolve {sha}/{version}
// before it, and identity families like {path} resolve after their inputs).
func (gitverLeaf) Provides() []string {
	return []string{
		"version", "base", "major", "minor", "patch", "prerelease", "branch", "sha",
		"stagefreight", "var", "env", "commit", "project", "date", "datetime",
		"timestamp", "ci", "rand", "randhex",
	}
}

func (gitverLeaf) DependsOn() []string { return nil }

func (gitverLeaf) Resolve(values []string, c *Context) []string {
	if c == nil || c.Version == nil {
		return values // no version info → leaf pass is a no-op (matches gitver's own guard)
	}
	opts := gitver.ResolveOptions{ProjectDescription: c.Description}
	if c.Config != nil {
		// metadata.license is authoritative for {project.license} (over the LICENSE scan);
		// and when no description was injected explicitly (e.g. the scribe surface),
		// {project.description} falls back to the metadata block too.
		opts.ProjectLicense = c.Config.Metadata.License
		if opts.ProjectDescription == "" {
			opts.ProjectDescription = c.Config.Metadata.Description.Default.First()
		}
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = gitver.ResolveTemplateWithOpts(v, c.Version, c.RootDir, c.Vars, opts)
	}
	return out
}
