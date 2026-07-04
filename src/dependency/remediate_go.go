package dependency

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
)

// A vulnerable INDIRECT Go dependency is a true signal that transitive management has
// failed for it: the "bump direct → go mod tidy pulls the fix" assumption does not hold,
// because no direct parent requires a fixed version. remediateGoVuln routes that signal
// to the right fix, preferring the durable one:
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

// remediateGoVuln remediates ONE vulnerable indirect Go module dependency. It returns the
// applied remediation, or a non-empty skip reason (in which case nothing was applied).
func remediateGoVuln(ctx context.Context, gc goModCtx, dep freshness.Dependency, directNames map[string]bool, directTargets map[string]string) (AppliedUpdate, string) {
	fixed := maxFixedVersion(dep)
	if fixed == "" {
		return AppliedUpdate{}, "no known fixed version for advisories"
	}
	base := AppliedUpdate{Dep: dep, OldVer: dep.Current, UpdateType: "security", CVEsFixed: advisoryIDs(dep)}

	// ── Case 1: prefer a parent bump that dissolves the vuln ────────────────────────
	// Worth attempting only when the responsible direct parent actually has a compatible
	// update to move to; otherwise there's nothing to bump and we pin directly.
	if parent := responsibleParentModule(ctx, gc, dep.Name, directNames); parent != "" {
		if target := directTargets[parent]; target != "" {
			if snap, ok := snapshotGoMod(gc.modDir); ok {
				if _, err := gc.run(ctx, "get", parent+"@"+goVersionQuery(target)); err == nil {
					if _, err := gc.run(ctx, "mod", "tidy"); err == nil {
						if sel := selectedVersion(ctx, gc, dep.Name); sel != "" &&
							freshness.CompareVersions(sel, fixed, dep.Ecosystem) >= 0 {
							u := base
							u.NewVer = sel
							u.Remediation = fmt.Sprintf("parent bump: %s→%s pulled %s to %s", parent, target, dep.Name, sel)
							return u, ""
						}
					}
				}
				restoreGoMod(snap) // reached only when the trial did NOT clear the vuln — revert, then pin
			}
		}
	}

	// ── Case 2: pin the fixed version as an explicit require (self-expiring floor) ──
	pinVer := goVersionQuery(fixed)
	if out, err := gc.run(ctx, "get", dep.Name+"@"+pinVer); err != nil {
		return AppliedUpdate{}, fmt.Sprintf("go get %s@%s: %s", dep.Name, pinVer, oneLine(out))
	}
	if out, err := gc.run(ctx, "mod", "tidy"); err != nil {
		return AppliedUpdate{}, fmt.Sprintf("go mod tidy after pinning %s: %s", dep.Name, oneLine(out))
	}
	u := base
	u.NewVer = pinVer
	u.Remediation = fmt.Sprintf("pinned floor: %s@%s (no parent update clears it)", dep.Name, pinVer)
	return u, ""
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
func maxFixedVersion(dep freshness.Dependency) string {
	best := ""
	for _, v := range dep.Vulnerabilities {
		f := strings.TrimSpace(v.FixedIn)
		if f == "" {
			continue
		}
		if best == "" || freshness.CompareVersions(f, best, dep.Ecosystem) > 0 {
			best = f
		}
	}
	return best
}

// advisoryIDs collects the advisory IDs a remediation clears (for reporting).
func advisoryIDs(dep freshness.Dependency) []string {
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
