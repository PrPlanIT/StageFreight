package dependency

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// A vulnerable INDIRECT Go dependency is a true signal that transitive management has
// failed for it: the "bump direct → go mod tidy pulls the fix" assumption does not hold,
// because no direct parent requires a fixed version. The vuln path (tryParentBump +
// batchPinVulns) routes that signal to the right fix, preferring the durable one:
//
//	Case 1 (parent bump) — if the direct dependency responsible for pulling the module
//	  (per `go mod why`) has a COMPATIBLE update that raises the module to a fixed
//	  version, apply that. The transitive vuln dissolves and no pin is needed.
//	Case 2 (pin floor)   — otherwise `go get <module>@<fixed>` writes an explicit require
//	  at the fixed version: a self-expiring floor MVS overtakes once a parent catches up.
//
// Go's own toolchain is the graph engine (go mod why / go get / go list); there is no
// MVS reimplementation here.

// goModCtx binds a goRunner to one module's invocation context (workspace mode uses -C).
type goModCtx struct {
	runGo        goRunner
	wd           string // working dir: repo root in workspace mode, else the module dir
	moduleRel    string // -C target (repoRoot-relative) in workspace mode
	modDir       string // physical dir holding this module's go.mod/go.sum (for snapshotting)
	hasWorkspace bool
}

func (g goModCtx) run(ctx context.Context, args ...string) ([]byte, error) {
	if g.hasWorkspace {
		return g.runGo(ctx, g.wd, append([]string{"-C", g.moduleRel}, args...)...)
	}
	return g.runGo(ctx, g.wd, args...)
}

// vulnPin is a case-2 remediation target: pin dep to at least its fixed version.
type vulnPin struct {
	dep   supplychain.Dependency
	fixed string // OSV fixed version (no "v" prefix)
}

// tryParentBump attempts CASE 1 for one vulnerable indirect: if the direct dependency
// responsible for pulling it has a COMPATIBLE update that raises the module to >= fixed,
// apply that — the vuln dissolves with no pin. Returns (update, true) on success, or
// (_, false) to fall through to a case-2 pin. A failed trial is reverted to a clean tree.
func tryParentBump(ctx context.Context, gc goModCtx, dep supplychain.Dependency, fixed string, directNames map[string]bool, directTargets map[string]string) (AppliedUpdate, bool) {
	parent := responsibleParentModule(ctx, gc, dep.Name, directNames)
	if parent == "" {
		return AppliedUpdate{}, false
	}
	target := directTargets[parent]
	if target == "" {
		return AppliedUpdate{}, false // parent has no compatible update — nothing to bump
	}
	snap, ok := snapshotGoMod(gc.modDir)
	if !ok {
		return AppliedUpdate{}, false
	}
	if _, err := gc.run(ctx, "get", parent+"@"+goVersionQuery(target)); err == nil {
		if _, err := gc.run(ctx, "mod", "tidy"); err == nil {
			if sel := selectedVersion(ctx, gc, dep.Name); sel != "" &&
				version.CompareVersions(sel, fixed, dep.Ecosystem) >= 0 {
				return AppliedUpdate{
					Dep: dep, OldVer: dep.Current, NewVer: sel, UpdateType: "security",
					CVEsFixed:   advisoryIDs(dep),
					Remediation: fmt.Sprintf("parent bump: %s→%s pulled %s to %s", parent, target, dep.Name, sel),
				}, true
			}
		}
	}
	restoreGoMod(snap) // trial didn't clear it — revert to a clean tree; caller pins instead
	return AppliedUpdate{}, false
}

// batchPinVulns applies CASE 2 for all pins as ONE consistent set. A security pin is a
// FLOOR (>= fix): pinning deps individually can force a downgrade cascade when one fix
// requires a newer version of another (e.g. x/net@0.55 requires x/sys@0.45, so pinning
// x/sys to its own 0.44 drags x/net back down to 0.54). So every pin goes in a single
// `go get`, and a "requires Y@vN" conflict bumps Y to vN — still >= Y's own fix — and
// retries. After one tidy, each pin's ACHIEVED version is re-read: applied only if it
// reached its fix (report reality), else skipped. Never a claimed fix that didn't land.
func batchPinVulns(ctx context.Context, gc goModCtx, pins []vulnPin) ([]AppliedUpdate, []SkippedDep) {
	targets := make(map[string]string, len(pins)) // module -> version query (floor)
	for _, p := range pins {
		targets[p.dep.Name] = goVersionQuery(p.fixed)
	}

	const maxRetry = 12
	var lastErr string
	resolved := false
	for i := 0; i < maxRetry; i++ {
		args := make([]string, 0, len(targets)+1)
		args = append(args, "get")
		for mod, ver := range targets {
			args = append(args, mod+"@"+ver)
		}
		out, err := gc.run(ctx, args...)
		if err == nil {
			resolved = true
			break
		}
		lastErr = oneLine(out)
		mod, ver := parseGoGetConflict(out)
		if mod == "" {
			break // not a version conflict we can resolve by bumping
		}
		targets[mod] = ver // raise to the required version (monotonic; go reports it > current)
	}
	if !resolved {
		return nil, skipAll(pins, "batch pin failed: "+lastErr)
	}
	if out, err := gc.run(ctx, "mod", "tidy"); err != nil {
		return nil, skipAll(pins, "go mod tidy after pinning failed: "+oneLine(out))
	}

	var applied []AppliedUpdate
	var skipped []SkippedDep
	for _, p := range pins {
		got := selectedVersion(ctx, gc, p.dep.Name)
		if got == "" || version.CompareVersions(got, p.fixed, p.dep.Ecosystem) < 0 {
			skipped = append(skipped, SkippedDep{Dep: p.dep,
				Reason: fmt.Sprintf("pin did not hold: got %q, need >= %s", got, p.fixed)})
			continue
		}
		applied = append(applied, AppliedUpdate{
			Dep: p.dep, OldVer: p.dep.Current, NewVer: got, UpdateType: "security",
			CVEsFixed:   advisoryIDs(p.dep),
			Remediation: fmt.Sprintf("pinned floor: %s@%s", p.dep.Name, got),
		})
	}
	return applied, skipped
}

func skipAll(pins []vulnPin, reason string) []SkippedDep {
	sk := make([]SkippedDep, 0, len(pins))
	for _, p := range pins {
		sk = append(sk, SkippedDep{Dep: p.dep, Reason: reason})
	}
	return sk
}

var reGoGetConflict = regexp.MustCompile(`requires\s+(\S+)@(v[0-9][^,\s]*),\s+not\b`)

// parseGoGetConflict extracts (module, requiredVersion) from a go get version conflict:
//
//	go: golang.org/x/net@v0.55.0 requires golang.org/x/sys@v0.45.0, not golang.org/x/sys@v0.44.0
//
// The module named after "requires" must be raised to that version. Returns "","" when the
// output is not such a conflict.
func parseGoGetConflict(out []byte) (module, version string) {
	m := reGoGetConflict.FindSubmatch(out)
	if m == nil {
		return "", ""
	}
	return string(m[1]), string(m[2])
}

// goVersionQuery makes a version string a valid `go get module@version` query. OSV reports
// fixed versions without the module "v" prefix (e.g. "0.55.0"); go get requires it
// ("v0.55.0"). Pseudo-versions, branch names, and already-prefixed versions pass through.
func goVersionQuery(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return v
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// maxFixedVersion returns the highest FixedIn across dep's advisories — the version that
// clears ALL of them. Empty when no advisory reports a fixed version.
func maxFixedVersion(dep supplychain.Dependency) string {
	best := ""
	for _, v := range dep.Vulnerabilities {
		f := strings.TrimSpace(v.FixedIn)
		if f == "" {
			continue
		}
		if best == "" || version.CompareVersions(f, best, dep.Ecosystem) > 0 {
			best = f
		}
	}
	return best
}

// advisoryIDs collects the advisory IDs a remediation clears (for reporting).
func advisoryIDs(dep supplychain.Dependency) []string {
	if len(dep.Vulnerabilities) == 0 {
		return nil
	}
	ids := make([]string, 0, len(dep.Vulnerabilities))
	for _, v := range dep.Vulnerabilities {
		ids = append(ids, v.ID)
	}
	return ids
}

// responsibleParentModule returns the direct dependency responsible for pulling `module`
// into the build. `go mod why -m` prints the shortest import chain; the first hop whose
// module is a known direct dependency is the parent whose bump could dissolve the vuln.
// Empty when the module is unused, or no hop maps to a known direct dep.
func responsibleParentModule(ctx context.Context, gc goModCtx, module string, directNames map[string]bool) string {
	out, err := gc.run(ctx, "mod", "why", "-m", module)
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "(") {
			continue // header, blank, or "(main module does not need module ...)"
		}
		if m := matchDirectModule(ln, directNames); m != "" {
			return m
		}
	}
	return ""
}

// matchDirectModule returns the longest known direct-module path that is a prefix of the
// package path pkg (module paths can nest), or "" when none matches.
func matchDirectModule(pkg string, directNames map[string]bool) string {
	best := ""
	for name := range directNames {
		if (pkg == name || strings.HasPrefix(pkg, name+"/")) && len(name) > len(best) {
			best = name
		}
	}
	return best
}

// selectedVersion returns the version MVS currently selects for module (`go list -m`).
func selectedVersion(ctx context.Context, gc goModCtx, module string) string {
	out, err := gc.run(ctx, "list", "-m", "-f", "{{.Version}}", module)
	if err != nil {
		return ""
	}
	return lastLine(out)
}

// goModSnapshot captures go.mod/go.sum so a case-1 trial that fails to clear the vuln can
// be reverted to a clean tree before pinning.
type goModSnapshot struct {
	modPath, sumPath string
	mod, sum         []byte
	sumExisted       bool
}

func snapshotGoMod(modDir string) (goModSnapshot, bool) {
	modPath := filepath.Join(modDir, "go.mod")
	mod, err := os.ReadFile(modPath)
	if err != nil {
		return goModSnapshot{}, false
	}
	sumPath := filepath.Join(modDir, "go.sum")
	sum, sumErr := os.ReadFile(sumPath)
	return goModSnapshot{modPath: modPath, sumPath: sumPath, mod: mod, sum: sum, sumExisted: sumErr == nil}, true
}

func restoreGoMod(s goModSnapshot) {
	if s.modPath == "" {
		return
	}
	_ = os.WriteFile(s.modPath, s.mod, 0o644)
	if s.sumExisted {
		_ = os.WriteFile(s.sumPath, s.sum, 0o644)
	} else {
		_ = os.Remove(s.sumPath)
	}
}

func oneLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}
