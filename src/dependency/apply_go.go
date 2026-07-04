package dependency

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// goRunner executes a go subcommand in the given directory.
type goRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// resolveGoRunner resolves a Go toolchain via the StageFreight toolchain subsystem.
// Downloads, verifies, and caches the binary if not already present.
// No host assumptions. No DinD. No containers-for-tools.
// resolveGoRunner is memoized per repo-root: a deps run resolves the Go toolchain
// from several phases (apply, verify), but we resolve + render the provisioning ledger
// exactly ONCE — a toolchain resolution is not a presentation event per call.
var (
	goRunnerMu   sync.Mutex
	goRunnerMemo = map[string]goRunner{}
)

func resolveGoRunner(repoRoot string) (goRunner, error) {
	goRunnerMu.Lock()
	defer goRunnerMu.Unlock()
	if r, ok := goRunnerMemo[repoRoot]; ok {
		return r, nil
	}
	version := toolchain.ResolveGoVersion(".", repoRoot)
	result, err := toolchain.Resolve(repoRoot, "go", version)
	if err != nil {
		return nil, fmt.Errorf("go toolchain: %w", err)
	}
	provision.Render(os.Stderr, []provision.Entry{provision.FromToolchain(result, "dependency update")}, output.UseColor())
	runner := goRunner(func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, result.Path, args...)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		return cmd.CombinedOutput()
	})
	goRunnerMemo[repoRoot] = runner
	return runner, nil
}

// applyGoUpdates applies Go module dependency updates. allDeps is the full resolved
// dependency set (direct + indirect) used by the vuln remediator to identify responsible
// direct parents and their compatible targets. Returns touched module dirs (repoRoot-
// relative) as the 3rd value, and touched files (go.mod, go.sum per module) as the 4th —
// only dirs/files where the update(s) succeeded.
func applyGoUpdates(ctx context.Context, deps []freshness.Dependency, allDeps []freshness.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, []string, error) {
	// Check for go.work — workspace mode uses -C with relative paths
	hasWorkspace := false
	if _, err := os.Stat(filepath.Join(repoRoot, "go.work")); err == nil {
		hasWorkspace = true
	}

	// Direct-dependency map for the vuln remediator: which Go modules are DIRECT (a
	// parent-bump candidate for case-1 remediation) and their compatible update target.
	directNames := make(map[string]bool)
	directTargets := make(map[string]string)
	for _, d := range allDeps {
		if d.Ecosystem != freshness.EcosystemGoMod || d.Indirect {
			continue
		}
		directNames[d.Name] = true
		if t := d.UpdateTarget(); t != "" && t != d.Current {
			directTargets[d.Name] = t
		}
	}

	// Group deps by module dir (derived from dep.File)
	type moduleGroup struct {
		dir  string // repoRoot-relative
		deps []freshness.Dependency
	}
	groupMap := make(map[string]*moduleGroup)
	for _, dep := range deps {
		dir := filepath.Dir(dep.File)
		if g, ok := groupMap[dir]; ok {
			g.deps = append(g.deps, dep)
		} else {
			groupMap[dir] = &moduleGroup{dir: dir, deps: []freshness.Dependency{dep}}
		}
	}

	var applied []AppliedUpdate
	var skipped []SkippedDep
	touchedSet := make(map[string]struct{})

	// Pre-filter BEFORE resolving any toolchain: drop content/tooling modules with no
	// Go source. `go get`/`go mod tidy` would CORRUPT them — tidy deletes every require
	// no Go file imports (a Hugo theme module's hextra is consumed by Hugo, never
	// imported), so an "update" silently removes the dependency. And resolving a Go
	// toolchain only to skip every candidate is wasted work.
	var actionable []*moduleGroup
	for _, group := range groupMap {
		// Guard: module dir must be safely under repo root
		if strings.HasPrefix(group.dir, "..") || filepath.IsAbs(group.dir) {
			return applied, skipped, nil, nil, fmt.Errorf("module dir %q escapes repo root", group.dir)
		}
		if !moduleHasGoFiles(filepath.Join(repoRoot, group.dir)) {
			for _, dep := range group.deps {
				skipped = append(skipped, SkippedDep{Dep: dep, Reason: "no Go source (content/tooling module)"})
			}
			continue
		}
		actionable = append(actionable, group)
	}

	// Nothing actually applies — don't resolve a Go toolchain at all.
	if len(actionable) == 0 {
		return applied, skipped, nil, nil, nil
	}

	runGo, err := resolveGoRunner(repoRoot)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	for _, group := range actionable {
		modulePath := filepath.Join(repoRoot, group.dir)

		// Detect replace directives for this module
		replaceSet, err := detectReplaceDirectives(modulePath)
		if err != nil && len(replaceSet) == 0 {
			// Non-fatal — continue without replace detection
			replaceSet = nil
		}

		goDir := modulePath
		if hasWorkspace {
			goDir = repoRoot
		}
		gc := goModCtx{runGo: runGo, wd: goDir, moduleRel: group.dir, modDir: modulePath, hasWorkspace: hasWorkspace}

		// Split candidates: vulnerable indirects route through the security remediator
		// (parent-bump-or-pin, FixedIn target — see remediate_go.go); everything else is a
		// normal batched `go get @Latest`.
		var normal, vulnIndirect []freshness.Dependency
		for _, dep := range group.deps {
			if replaceSet != nil && replaceSet[dep.Name] {
				skipped = append(skipped, SkippedDep{Dep: dep, Reason: "replace directive present"})
				continue
			}
			if dep.Indirect && len(dep.Vulnerabilities) > 0 {
				vulnIndirect = append(vulnIndirect, dep)
			} else {
				normal = append(normal, dep)
			}
		}

		// Normal batch: go get @Latest for all, then a single tidy.
		if len(normal) > 0 {
			var getArgs []string
			var pending []AppliedUpdate
			for _, dep := range normal {
				getArgs = append(getArgs, dep.Name+"@"+dep.Latest)
				u := AppliedUpdate{Dep: dep, OldVer: dep.Current, NewVer: dep.Latest, UpdateType: updateType(dep.Current, dep.Latest)}
				for _, v := range dep.Vulnerabilities {
					u.CVEsFixed = append(u.CVEsFixed, v.ID)
				}
				pending = append(pending, u)
			}
			if out, err := gc.run(ctx, append([]string{"get"}, getArgs...)...); err != nil {
				return applied, skipped, nil, nil, fmt.Errorf("go get in %s: %s\n%w", group.dir, string(out), err)
			}
			if out, err := gc.run(ctx, "mod", "tidy"); err != nil {
				return applied, skipped, nil, nil, fmt.Errorf("go mod tidy in %s: %s\n%w", group.dir, string(out), err)
			}
			applied = append(applied, pending...)
			touchedSet[group.dir] = struct{}{}
		}

		// Vulnerable indirects: routed remediation (parent-bump preferred, pin fallback).
		for _, dep := range vulnIndirect {
			u, reason := remediateGoVuln(ctx, gc, dep, directNames, directTargets)
			if reason != "" {
				skipped = append(skipped, SkippedDep{Dep: dep, Reason: reason})
				continue
			}
			applied = append(applied, u)
			touchedSet[group.dir] = struct{}{}
		}
	}

	touchedDirs := make([]string, 0, len(touchedSet))
	var touchedFiles []string
	for d := range touchedSet {
		touchedDirs = append(touchedDirs, d)
		touchedFiles = append(touchedFiles, filepath.Join(d, "go.mod"))
		touchedFiles = append(touchedFiles, filepath.Join(d, "go.sum"))
	}
	sort.Strings(touchedDirs)
	sort.Strings(touchedFiles)
	return applied, skipped, touchedDirs, touchedFiles, nil
}

// goDirectiveSyncTarget maps a Dockerfile golang builder update to its owning module.
type goDirectiveSyncTarget struct {
	ModuleDir string // repo-relative dir containing go.mod ("." for root)
	GoVersion string // target Go version from the builder image (full patch)
	Source    string // Dockerfile path that triggered the sync
}

// ToolchainDependency records a resolved build toolchain for reporting and SBOM.
type ToolchainDependency struct {
	Ecosystem    string // "golang"
	Name         string // "go"
	Version      string // "1.26.1"
	BuilderImage string // "docker.io/library/golang:1.26.1-alpine3.23"
	Dockerfile   string // repo-relative Dockerfile path
	ModuleDir    string // repo-relative module dir
}

// hasAppliedGolangBuilderUpdate returns true if any applied update was a golang builder image.
func hasAppliedGolangBuilderUpdate(applied []AppliedUpdate) bool {
	for _, a := range applied {
		if a.Dep.Ecosystem == freshness.EcosystemDockerImage && isGolangImage(a.Dep.Name) {
			return true
		}
	}
	return false
}

// syncGoDirectivesFromResolved bumps go directives in go.mod files to match their
// associated Dockerfile golang builder versions, using pre-computed sync targets.
func syncGoDirectivesFromResolved(ctx context.Context, repoRoot string, result *UpdateResult, resolved goDirectiveSyncResult) error {
	// Surface conflicted modules as skipped entries with version details
	for _, conflict := range resolved.Conflicted {
		detail := fmt.Sprintf("conflicting golang builder versions in %s: %s (from %s)",
			conflict.ModuleDir,
			strings.Join(conflict.Versions, " vs "),
			strings.Join(conflict.Sources, ", "))
		result.Skipped = append(result.Skipped, SkippedDep{
			Dep: freshness.Dependency{
				Name:      "stdlib",
				Ecosystem: freshness.EcosystemGoMod,
				File:      moduleGoModPath(conflict.ModuleDir),
			},
			Reason: detail,
		})
	}

	if len(resolved.Targets) == 0 {
		return nil
	}

	runGo, err := resolveGoRunner(repoRoot)
	if err != nil {
		return err
	}

	for _, t := range resolved.Targets {
		modFile := filepath.Join(repoRoot, t.ModuleDir, "go.mod")
		if _, err := os.Stat(modFile); err != nil {
			continue
		}

		cur := parseGoDirectiveFromFile(modFile)
		if cur == "" || cur == t.GoVersion {
			continue
		}

		absDir := filepath.Join(repoRoot, t.ModuleDir)

		out, err := runGo(ctx, absDir, "mod", "edit", "-go="+t.GoVersion)
		if err != nil {
			return fmt.Errorf("go mod edit -go=%s in %s: %s\n%w", t.GoVersion, t.ModuleDir, string(out), err)
		}

		out, err = runGo(ctx, absDir, "mod", "tidy")
		if err != nil {
			return fmt.Errorf("go mod tidy in %s: %s\n%w", t.ModuleDir, string(out), err)
		}

		result.Applied = append(result.Applied, AppliedUpdate{
			Dep: freshness.Dependency{
				Name:      "stdlib",
				Current:   cur,
				Latest:    t.GoVersion,
				Ecosystem: freshness.EcosystemGoMod,
				File:      moduleGoModPath(t.ModuleDir),
			},
			OldVer:     cur,
			NewVer:     t.GoVersion,
			UpdateType: updateType(cur, t.GoVersion),
		})

		found := false
		for _, d := range result.TouchedModuleDirs {
			if d == t.ModuleDir {
				found = true
				break
			}
		}
		if !found {
			result.TouchedModuleDirs = append(result.TouchedModuleDirs, t.ModuleDir)
		}
	}

	return nil
}

// goDirectiveConflict records a module with conflicting golang builder versions.
type goDirectiveConflict struct {
	ModuleDir string   // repo-relative module dir
	Versions  []string // the conflicting Go versions (e.g. ["1.26.1", "1.25.7"])
	Sources   []string // Dockerfile paths that caused the conflict
}

// goDirectiveSyncResult holds resolved targets and any conflicted modules.
type goDirectiveSyncResult struct {
	Targets    []goDirectiveSyncTarget
	Conflicted []goDirectiveConflict
}

// collectGoDirectiveSyncTargets maps applied golang builder Docker updates to
// their owning Go module directories. Each Dockerfile is mapped to the nearest
// ancestor directory containing a go.mod file.
//
// If two Dockerfiles in the same module want different Go versions, the module
// is marked conflicted and skipped entirely — no silent winner-picking.
func collectGoDirectiveSyncTargets(repoRoot string, applied []AppliedUpdate) goDirectiveSyncResult {
	byModuleDir := make(map[string]goDirectiveSyncTarget)
	conflicted := make(map[string]bool)

	for _, a := range applied {
		if a.Dep.Ecosystem != freshness.EcosystemDockerImage || !isGolangImage(a.Dep.Name) {
			continue
		}

		goVer := extractGoVersionFromTag(a.NewVer)
		if goVer == "" {
			continue
		}

		moduleDir := findNearestGoMod(repoRoot, filepath.Dir(a.Dep.File))
		if moduleDir == "" {
			continue
		}

		if conflicted[moduleDir] {
			continue
		}

		if existing, ok := byModuleDir[moduleDir]; ok {
			if existing.GoVersion != goVer {
				delete(byModuleDir, moduleDir)
				conflicted[moduleDir] = true
				continue
			}
		}

		byModuleDir[moduleDir] = goDirectiveSyncTarget{
			ModuleDir: moduleDir,
			GoVersion: goVer,
			Source:    a.Dep.File,
		}
	}

	targets := make([]goDirectiveSyncTarget, 0, len(byModuleDir))
	for _, t := range byModuleDir {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].ModuleDir < targets[j].ModuleDir
	})

	var conflicts []goDirectiveConflict
	for dir := range conflicted {
		conflicts = append(conflicts, collectConflictDetail(applied, repoRoot, dir))
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].ModuleDir < conflicts[j].ModuleDir
	})

	return goDirectiveSyncResult{Targets: targets, Conflicted: conflicts}
}

// collectToolchainDepsFromResolved builds toolchain dependency records from
// pre-computed sync targets, ensuring toolchain metadata matches what was synced.
func collectToolchainDepsFromResolved(resolved goDirectiveSyncResult, applied []AppliedUpdate) []ToolchainDependency {
	if len(resolved.Targets) == 0 {
		return nil
	}

	// Index applied golang builder updates by Dockerfile path
	bySource := make(map[string]AppliedUpdate)
	for _, a := range applied {
		if a.Dep.Ecosystem == freshness.EcosystemDockerImage && isGolangImage(a.Dep.Name) {
			bySource[a.Dep.File] = a
		}
	}

	deps := make([]ToolchainDependency, 0, len(resolved.Targets))
	for _, t := range resolved.Targets {
		a, ok := bySource[t.Source]
		if !ok {
			continue
		}
		deps = append(deps, ToolchainDependency{
			Ecosystem:    "golang",
			Name:         "go",
			Version:      t.GoVersion,
			BuilderImage: a.Dep.Name + ":" + a.NewVer,
			Dockerfile:   t.Source,
			ModuleDir:    t.ModuleDir,
		})
	}
	return deps
}

// collectConflictDetail gathers the specific versions and Dockerfiles that caused
// a conflict for a given module directory.
func collectConflictDetail(applied []AppliedUpdate, repoRoot, moduleDir string) goDirectiveConflict {
	seenVer := make(map[string]bool)
	var versions []string
	var sources []string

	for _, a := range applied {
		if a.Dep.Ecosystem != freshness.EcosystemDockerImage || !isGolangImage(a.Dep.Name) {
			continue
		}
		goVer := extractGoVersionFromTag(a.NewVer)
		if goVer == "" {
			continue
		}
		dir := findNearestGoMod(repoRoot, filepath.Dir(a.Dep.File))
		if dir != moduleDir {
			continue
		}
		sources = append(sources, a.Dep.File)
		if !seenVer[goVer] {
			seenVer[goVer] = true
			versions = append(versions, goVer)
		}
	}

	sort.Strings(versions)
	sort.Strings(sources)

	return goDirectiveConflict{
		ModuleDir: moduleDir,
		Versions:  versions,
		Sources:   sources,
	}
}

// findNearestGoMod walks up from relDir toward repoRoot looking for a go.mod file.
// Returns the repo-relative directory containing go.mod, or "" if not found.
func findNearestGoMod(repoRoot, relDir string) string {
	dir := relDir
	for {
		candidate := filepath.Join(repoRoot, dir, "go.mod")
		if _, err := os.Stat(candidate); err == nil {
			return dir
		}
		if dir == "." || dir == "" {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err == nil {
		return "."
	}
	return ""
}

// moduleGoModPath returns a clean repo-relative path to go.mod for a module dir.
func moduleGoModPath(moduleDir string) string {
	if moduleDir == "." || moduleDir == "" {
		return "go.mod"
	}
	return filepath.Join(moduleDir, "go.mod")
}

// isGolangImage returns true if the image name is a golang builder.
// Handles names with or without tags (e.g. "docker.io/library/golang:1.26.1-alpine3.23").
func isGolangImage(name string) bool {
	n := strings.ToLower(name)
	if idx := strings.LastIndex(n, ":"); idx > 0 {
		n = n[:idx]
	}
	return n == "golang" || n == "library/golang" ||
		strings.HasSuffix(n, "/golang") || strings.HasSuffix(n, "/library/golang")
}

// extractGoVersionFromTag extracts the Go version from a Docker tag.
// Returns the full patch version (e.g. "1.26.1") — the go directive should
// reflect the exact stdlib version used by the builder for accurate CVE scanning.
func extractGoVersionFromTag(tag string) string {
	ver := tag
	if idx := strings.IndexByte(ver, '-'); idx > 0 {
		ver = ver[:idx]
	}
	for _, c := range ver {
		if c != '.' && (c < '0' || c > '9') {
			return ""
		}
	}
	if ver == "" {
		return ""
	}
	return ver
}

// detectGoDirectiveDrift scans all resolved dependencies for golang builder images
// whose version is strictly newer than the corresponding go.mod directive. Returns
// sync targets for modules where the Dockerfile's golang version exceeds go.mod.
func detectGoDirectiveDrift(repoRoot string, allDeps []freshness.Dependency) goDirectiveSyncResult {
	byModuleDir := make(map[string]goDirectiveSyncTarget)
	conflicted := make(map[string]bool)

	for _, dep := range allDeps {
		if dep.Ecosystem != freshness.EcosystemDockerImage || !isGolangImage(dep.Name) {
			continue
		}

		goVer := extractGoVersionFromTag(dep.Current)
		if goVer == "" {
			continue
		}

		moduleDir := findNearestGoMod(repoRoot, filepath.Dir(dep.File))
		if moduleDir == "" {
			continue
		}

		if conflicted[moduleDir] {
			continue
		}

		// Only sync when builder is strictly newer than go.mod
		modFile := filepath.Join(repoRoot, moduleDir, "go.mod")
		cur := parseGoDirectiveFromFile(modFile)
		if cur == "" {
			continue
		}
		delta := freshness.CompareDependencyVersions(cur, goVer, freshness.EcosystemGoMod)
		if delta.Major <= 0 && delta.Minor <= 0 && delta.Patch <= 0 {
			continue // go.mod is equal or newer — no drift
		}

		if existing, ok := byModuleDir[moduleDir]; ok {
			if existing.GoVersion != goVer {
				delete(byModuleDir, moduleDir)
				conflicted[moduleDir] = true
				continue
			}
		}

		byModuleDir[moduleDir] = goDirectiveSyncTarget{
			ModuleDir: moduleDir,
			GoVersion: goVer,
			Source:    dep.File,
		}
	}

	targets := make([]goDirectiveSyncTarget, 0, len(byModuleDir))
	for _, t := range byModuleDir {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].ModuleDir < targets[j].ModuleDir
	})

	var conflicts []goDirectiveConflict
	for dir := range conflicted {
		conflicts = append(conflicts, goDirectiveConflict{ModuleDir: dir})
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].ModuleDir < conflicts[j].ModuleDir
	})

	return goDirectiveSyncResult{Targets: targets, Conflicted: conflicts}
}

// mergeGoDirectiveSyncResults combines two sync results, deduplicating by module dir.
// If both sources have an entry for the same module (target or conflict), the first (a) wins.
func mergeGoDirectiveSyncResults(a, b goDirectiveSyncResult) goDirectiveSyncResult {
	if len(b.Targets) == 0 && len(b.Conflicted) == 0 {
		return a
	}
	if len(a.Targets) == 0 && len(a.Conflicted) == 0 {
		return b
	}

	seen := make(map[string]bool)
	for _, t := range a.Targets {
		seen[t.ModuleDir] = true
	}
	for _, c := range a.Conflicted {
		seen[c.ModuleDir] = true
	}

	for _, t := range b.Targets {
		if !seen[t.ModuleDir] {
			a.Targets = append(a.Targets, t)
			seen[t.ModuleDir] = true
		}
	}
	for _, c := range b.Conflicted {
		if !seen[c.ModuleDir] {
			a.Conflicted = append(a.Conflicted, c)
			seen[c.ModuleDir] = true
		}
	}

	sort.Slice(a.Targets, func(i, j int) bool {
		return a.Targets[i].ModuleDir < a.Targets[j].ModuleDir
	})
	sort.Slice(a.Conflicted, func(i, j int) bool {
		return a.Conflicted[i].ModuleDir < a.Conflicted[j].ModuleDir
	})

	return a
}

// parseGoDirectiveFromFile reads a go.mod or go.work file and returns the go version directive.
// Used for comparing current vs desired Go versions in module sync — not for toolchain resolution.
func parseGoDirectiveFromFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var goVer string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "toolchain ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				ver := strings.TrimPrefix(fields[1], "go")
				if parts := strings.SplitN(ver, ".", 3); len(parts) >= 2 {
					return parts[0] + "." + parts[1]
				}
				return ver
			}
		}
		if goVer == "" && strings.HasPrefix(line, "go ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				goVer = fields[1]
			}
		}
	}
	return goVer
}

// detectReplaceDirectives parses go.mod and returns a set of replaced module paths.
func detectReplaceDirectives(moduleDir string) (map[string]bool, error) {
	gomod := filepath.Join(moduleDir, "go.mod")
	f, err := os.Open(gomod)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	replaced := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	inReplace := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "replace (") || strings.HasPrefix(line, "replace(") {
			inReplace = true
			continue
		}
		if inReplace {
			if line == ")" {
				inReplace = false
				continue
			}
			// Inside replace block: "module => replacement"
			parts := strings.Fields(line)
			if len(parts) >= 3 && parts[1] == "=>" {
				replaced[parts[0]] = true
			}
			continue
		}
		if strings.HasPrefix(line, "replace ") {
			// Single-line replace: "replace module => replacement"
			parts := strings.Fields(line)
			if len(parts) >= 4 && parts[2] == "=>" {
				replaced[parts[1]] = true
			}
		}
	}

	return replaced, scanner.Err()
}

// updateType determines the semver update type between two versions.
func updateType(current, latest string) string {
	delta := freshness.CompareDependencyVersions(current, latest, freshness.EcosystemGoMod)
	if delta.IsZero() {
		return "tag"
	}
	if delta.Major > 0 {
		return "major"
	}
	if delta.Minor > 0 {
		return "minor"
	}
	return "patch"
}
