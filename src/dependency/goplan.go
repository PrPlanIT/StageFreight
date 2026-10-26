package dependency

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// goRunner executes a go subcommand in the given directory.
type goRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// goPlan is a dependency pass's single decision about Go: the version each module runs
// under, made once before any go command, and one provisioned runner per version. Every
// Go step of the pass — directive sync, module updates, verify — runs through it, so a
// pass cannot run a module under two Gos, and the go command never fetches a toolchain
// of its own (GOTOOLCHAIN=local): StageFreight's verified Go is the only one that runs.
type goPlan struct {
	versions map[string]string   // repo-relative module dir → planned Go version
	runners  map[string]goRunner // planned version → runner
}

// runner is the Go a module runs under, or nil when the plan has no Go for it.
func (p goPlan) runner(moduleDir string) goRunner {
	return p.runners[p.versions[moduleDir]]
}

// planGoVersions decides each module's Go: the directive target when the pass raises
// it, else what the module declares (its toolchain line, then its go line). A go.work
// workspace runs every module under the highest of them, since its commands run at the
// root.
func planGoVersions(repoRoot string, moduleDirs []string, sync goDirectiveSyncResult) map[string]string {
	targets := map[string]string{}
	for _, t := range sync.Targets {
		targets[t.ModuleDir] = t.GoVersion
	}
	versions := map[string]string{}
	plan := func(dir string) {
		if _, done := versions[dir]; done {
			return
		}
		v := toolchain.ResolveGoVersion(filepath.Join(repoRoot, dir), repoRoot)
		if t := targets[dir]; t != "" && version.CompareVersions(t, v, supplychain.EcosystemGoMod) > 0 {
			v = t
		}
		versions[dir] = v
	}
	for _, d := range moduleDirs {
		plan(d)
	}
	for d := range targets {
		plan(d)
	}
	if hasGoWorkspace(repoRoot) {
		highest := ""
		for _, v := range versions {
			if highest == "" || version.CompareVersions(v, highest, supplychain.EcosystemGoMod) > 0 {
				highest = v
			}
		}
		for d := range versions {
			versions[d] = highest
		}
	}
	return versions
}

// resolveGo provisions a verified Go distribution (a seam for tests).
var resolveGo = func(repoRoot, version string) (toolchain.Result, error) {
	return toolchain.Resolve(repoRoot, "go", version)
}

// provisionGoPlan stages each planned version once, shown as one Staged Tools card.
func provisionGoPlan(repoRoot string, versions map[string]string) (goPlan, error) {
	p := goPlan{versions: versions, runners: map[string]goRunner{}}
	var distinct []string
	for _, v := range versions {
		if _, seen := p.runners[v]; !seen {
			p.runners[v] = nil
			distinct = append(distinct, v)
		}
	}
	sort.Strings(distinct)
	var staged []provision.Entry
	for _, v := range distinct {
		res, err := resolveGo(repoRoot, v)
		if err != nil {
			return goPlan{}, fmt.Errorf("go toolchain %s: %w", v, err)
		}
		staged = append(staged, provision.FromToolchain(res, "dependency update"))
		bin := res.Path
		p.runners[v] = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
			return cmd.CombinedOutput()
		}
	}
	if len(staged) > 0 {
		provision.Render(os.Stderr, staged, output.UseColor())
	}
	return p, nil
}

// hasGoWorkspace reports a go.work at the repo root.
func hasGoWorkspace(repoRoot string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, "go.work"))
	return err == nil
}
