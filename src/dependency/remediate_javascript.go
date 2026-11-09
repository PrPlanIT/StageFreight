package dependency

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	masterminds "github.com/Masterminds/semver/v3"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// A vulnerable package that only a JavaScript lockfile names — a dependency of a
// dependency — has no manifest line for the ordinary update pass to bump. It is
// remediated in lanes, the first that clears it winning, each verified by re-scanning
// the lockfile so only a fix that actually landed is reported:
//
//	refresh  the package manager re-resolves just those packages within every declared
//	         range (npm update / yarn up -R / pnpm update --depth Infinity, lockfile only).
//	parent   a direct dependency's own update, applied by the ordinary pass before this.
//	override a floor in the manifest's overrides (npm, pnpm) or resolutions (yarn), under
//	         dependency.transitive.override; recorded in .stagefreight/overrides.lock so
//	         StageFreight removes it once the graph no longer needs it.
//
// Anything left is reported with what requires it, for a person to decide.

const (
	jsManagerNpm         = "npm"
	jsManagerYarn        = "yarn"
	jsManagerYarnClassic = "yarn-classic"
	jsManagerPnpm        = "pnpm"
)

// jsProject is one JavaScript lockfile and its manifest.
type jsProject struct {
	dir      string // absolute
	manifest string // repo-relative package.json
	lockfile string // repo-relative lockfile
	manager  string
}

// overridePath is the manifest key path holding this manager's overrides.
func (p jsProject) overridePath() []string {
	switch p.manager {
	case jsManagerNpm:
		return []string{"overrides"}
	case jsManagerPnpm:
		return []string{"pnpm", "overrides"}
	}
	return []string{"resolutions"}
}

// discoverJSProjects returns each tracked lockfile that sits beside a tracked
// package.json the dependency scan resolved (manifests — so lint.exclude bounds this
// pass exactly as it bounds the direct one), one per directory (pnpm, then yarn, then
// npm — the order the lockfile sync uses). A nested project is its own dependency graph
// (a docs site beside the app) unless an ancestor in the repo is a workspace root, whose
// lockfile resolves it.
func discoverJSProjects(repoRoot string, isTracked, manifests map[string]bool) []jsProject {
	rank := map[string]int{"pnpm-lock.yaml": 0, "yarn.lock": 1, "package-lock.json": 2}
	best := map[string]string{}
	for f := range isTracked {
		base := path.Base(f)
		if _, ok := rank[base]; !ok {
			continue
		}
		dir := path.Dir(f)
		if m := path.Join(dir, "package.json"); !isTracked[m] || !manifests[m] {
			continue
		}
		if underWorkspaceRoot(repoRoot, dir, isTracked) {
			continue
		}
		if cur, ok := best[dir]; !ok || rank[base] < rank[path.Base(cur)] {
			best[dir] = f
		}
	}
	var out []jsProject
	for dir, lock := range best {
		p := jsProject{
			dir:      filepath.Join(repoRoot, filepath.FromSlash(dir)),
			manifest: path.Join(dir, "package.json"),
			lockfile: lock,
		}
		switch path.Base(lock) {
		case "pnpm-lock.yaml":
			p.manager = jsManagerPnpm
		case "package-lock.json":
			p.manager = jsManagerNpm
		default:
			p.manager = jsManagerYarnClassic
			if data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(lock))); err == nil && bytes.Contains(data, []byte("\n__metadata:")) {
				p.manager = jsManagerYarn
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lockfile < out[j].lockfile })
	return out
}

// underWorkspaceRoot reports whether a repo directory above dir declares workspaces
// (package.json "workspaces", or pnpm-workspace.yaml) — making dir a member whose
// packages the root's lockfile resolves, and any lockfile of its own stale.
func underWorkspaceRoot(repoRoot, dir string, isTracked map[string]bool) bool {
	for dir != "." && dir != "/" && dir != "" {
		dir = path.Dir(dir)
		if isTracked[path.Join(dir, "pnpm-workspace.yaml")] {
			return true
		}
		manifest := path.Join(dir, "package.json")
		if !isTracked[manifest] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(manifest)))
		if err != nil {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(data, &m) == nil && m["workspaces"] != nil {
			return true
		}
	}
	return false
}

// jsVuln is one vulnerable transitive package as a lockfile scan sees it.
type jsVuln struct {
	versions   []string
	advisories map[string]supplychain.VulnInfo // by ID
}

// fixable returns the advisory IDs that name a fixed version.
func (v *jsVuln) fixable() []string {
	var ids []string
	for id, a := range v.advisories {
		if a.FixedIn != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// jsScan is a lockfile's vulnerable transitive packages and every locked version.
type jsScan struct {
	vulns  map[string]*jsVuln
	copies map[string][]string
}

// remaining returns which of ids are still reported against name.
func (s jsScan) remaining(name string, ids []string) []string {
	v := s.vulns[name]
	if v == nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		if _, ok := v.advisories[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// jsRemediator carries what every project's remediation shares.
type jsRemediator struct {
	cfg      UpdateConfig
	repoRoot string
	scanner  string
	lock     *overridesLock
	tools    *nodeToolRunner
}

// remediateJSTransitive runs the lanes for every project and reports what landed.
func remediateJSTransitive(ctx context.Context, cfg UpdateConfig, repoRoot string, projects []jsProject) ([]AppliedUpdate, []SkippedDep, []string, error) {
	if cfg.OSVScanner == nil {
		return nil, nil, nil, fmt.Errorf("no osv-scanner configured to read the lockfiles")
	}
	scanner, err := cfg.OSVScanner(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("osv-scanner: %w", err)
	}
	lock, err := readOverridesLock(repoRoot)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading %s: %w", overridesLockPath(""), err)
	}
	before, _ := os.ReadFile(overridesLockPath(repoRoot))
	r := &jsRemediator{cfg: cfg, repoRoot: repoRoot, scanner: scanner, lock: lock}

	var applied []AppliedUpdate
	var skipped []SkippedDep
	var files []string
	for _, p := range projects {
		a, s, f, err := r.project(ctx, p)
		applied, skipped, files = append(applied, a...), append(skipped, s...), append(files, f...)
		if err != nil {
			return applied, skipped, files, fmt.Errorf("%s: %w", p.lockfile, err)
		}
	}
	if err := lock.write(repoRoot); err != nil {
		return applied, skipped, files, fmt.Errorf("writing %s: %w", overridesLockPath(""), err)
	}
	if after, _ := os.ReadFile(overridesLockPath(repoRoot)); !bytes.Equal(before, after) {
		files = append(files, overridesLockPath(""))
	}
	return applied, skipped, files, nil
}

// project remediates one lockfile. On any error the manifest and lockfile are restored.
func (r *jsRemediator) project(ctx context.Context, p jsProject) (applied []AppliedUpdate, skipped []SkippedDep, files []string, err error) {
	manifestAbs := filepath.Join(r.repoRoot, filepath.FromSlash(p.manifest))
	lockAbs := filepath.Join(r.repoRoot, filepath.FromSlash(p.lockfile))
	origManifest, err := os.ReadFile(manifestAbs)
	if err != nil {
		return nil, nil, nil, err
	}
	origLock, err := os.ReadFile(lockAbs)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() {
		if err != nil {
			os.WriteFile(manifestAbs, origManifest, 0o644)
			os.WriteFile(lockAbs, origLock, 0o644)
			return
		}
		if cur, _ := os.ReadFile(manifestAbs); !bytes.Equal(cur, origManifest) {
			files = append(files, p.manifest)
		}
		if cur, _ := os.ReadFile(lockAbs); !bytes.Equal(cur, origLock) {
			files = append(files, p.lockfile)
		}
	}()
	direct := manifestDependencyNames(origManifest)
	opath := p.overridePath()

	pruned, err := r.prune(ctx, p, manifestAbs)
	if err != nil {
		return nil, nil, nil, err
	}
	applied = append(applied, pruned...)

	scan, err := r.scan(ctx, p, lockAbs, direct)
	if err != nil {
		return applied, nil, nil, err
	}
	var cands []string
	for _, name := range sortedVulnNames(scan) {
		v := scan.vulns[name]
		if len(v.fixable()) == 0 {
			skipped = append(skipped, SkippedDep{Dep: jsDep(p, name, v), Category: SkipNoFixedVersion, Reason: "no fixed version published"})
			continue
		}
		cands = append(cands, name)
	}
	if len(cands) == 0 {
		return applied, skipped, nil, nil
	}
	if p.manager == jsManagerYarnClassic {
		for _, name := range cands {
			skipped = append(skipped, SkippedDep{Dep: jsDep(p, name, scan.vulns[name]), Category: SkipLockfileUnsupported,
				Reason: "yarn classic (v1) cannot re-resolve its lockfile without installing packages" + requiredBy(lockAbs, p.manager, name)})
		}
		return applied, skipped, nil, nil
	}

	// Lane: refresh within the declared ranges.
	start := scan
	if err := r.refresh(ctx, p, cands); err != nil {
		return applied, skipped, nil, fmt.Errorf("lockfile refresh: %w", err)
	}
	if scan, err = r.scan(ctx, p, lockAbs, direct); err != nil {
		return applied, skipped, nil, err
	}
	var rest []string
	for _, name := range cands {
		ids := start.vulns[name].fixable()
		if len(scan.remaining(name, ids)) == 0 {
			applied = append(applied, jsApplied(p, name, start, scan, ids, "lockfile refresh within the declared ranges"))
			continue
		}
		rest = append(rest, name)
	}

	// Lane: override floor.
	manifest, _ := os.ReadFile(manifestAbs)
	floors := map[string]overrideFloor{}
	for _, name := range rest {
		v := scan.vulns[name]
		ids := scan.remaining(name, start.vulns[name].fixable())
		fix := highestFix(v, ids)
		if reason := r.overrideHeld(p, manifest, name, fix, scan.copies[name]); reason != "" {
			skipped = append(skipped, SkippedDep{Dep: jsDep(p, name, v), Category: SkipOverrideHeld, Reason: reason + requiredBy(lockAbs, p.manager, name)})
			continue
		}
		next, err := setManifestOverride(manifest, opath, name, "^"+fix)
		if err != nil {
			return applied, skipped, nil, err
		}
		manifest = next
		floors[name] = overrideFloor{Manifest: p.manifest, Package: name, Floor: "^" + fix, Advisories: ids}
	}
	if len(floors) == 0 {
		return applied, skipped, nil, nil
	}
	if err := os.WriteFile(manifestAbs, manifest, 0o644); err != nil {
		return applied, skipped, nil, err
	}
	if err := r.sync(ctx, p); err != nil {
		return applied, skipped, nil, fmt.Errorf("lockfile sync after override: %w", err)
	}
	after, err := r.scan(ctx, p, lockAbs, direct)
	if err != nil {
		return applied, skipped, nil, err
	}
	var failed []string
	for name, f := range floors {
		if len(after.remaining(name, f.Advisories)) > 0 {
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		for _, name := range failed {
			if manifest, err = removeManifestOverride(manifest, opath, name); err != nil {
				return applied, skipped, nil, err
			}
			skipped = append(skipped, SkippedDep{Dep: jsDep(p, name, scan.vulns[name]), Category: SkipOverrideHeld,
				Reason: fmt.Sprintf("an override to %s did not clear it", floors[name].Floor) + requiredBy(lockAbs, p.manager, name)})
			delete(floors, name)
		}
		if err := os.WriteFile(manifestAbs, manifest, 0o644); err != nil {
			return applied, skipped, nil, err
		}
		if err := r.sync(ctx, p); err != nil {
			return applied, skipped, nil, fmt.Errorf("lockfile sync after reverting an override: %w", err)
		}
		if after, err = r.scan(ctx, p, lockAbs, direct); err != nil {
			return applied, skipped, nil, err
		}
	}
	for _, name := range sortedFloorNames(floors) {
		f := floors[name]
		r.lock.set(f)
		applied = append(applied, jsApplied(p, name, scan, after, f.Advisories, fmt.Sprintf("override floor %s %s in %s", name, f.Floor, p.manifest)))
	}
	return applied, skipped, nil, nil
}

// prune removes the floors StageFreight wrote for this manifest that the graph no
// longer needs, restoring any whose removal brings a vulnerability back. Entries an
// operator has since edited are dropped from the lock and left in the manifest.
func (r *jsRemediator) prune(ctx context.Context, p jsProject, manifestAbs string) ([]AppliedUpdate, error) {
	ours := r.lock.forManifest(p.manifest)
	if len(ours) == 0 || p.manager == jsManagerYarnClassic {
		return nil, nil
	}
	opath := p.overridePath()
	lockAbs := filepath.Join(r.repoRoot, filepath.FromSlash(p.lockfile))
	origManifest, err := os.ReadFile(manifestAbs)
	if err != nil {
		return nil, err
	}
	origLock, err := os.ReadFile(lockAbs)
	if err != nil {
		return nil, err
	}
	manifest := origManifest
	var trial []overrideFloor
	for _, o := range ours {
		if v, ok := manifestOverride(manifest, opath, o.Package); !ok || v != o.Floor {
			r.lock.drop(o.Manifest, o.Package)
			continue
		}
		if manifest, err = removeManifestOverride(manifest, opath, o.Package); err != nil {
			return nil, err
		}
		trial = append(trial, o)
	}
	if len(trial) == 0 {
		return nil, nil
	}
	if err := os.WriteFile(manifestAbs, manifest, 0o644); err != nil {
		return nil, err
	}
	if err := r.sync(ctx, p); err != nil {
		return nil, fmt.Errorf("lockfile sync while removing override floors: %w", err)
	}
	scan, err := r.scan(ctx, p, lockAbs, manifestDependencyNames(manifest))
	if err != nil {
		return nil, err
	}

	// Rebuild from the original bytes, removing only the floors the graph no longer
	// needs, so a floor that stays keeps its place in the file.
	final := origManifest
	var applied []AppliedUpdate
	for _, o := range trial {
		if len(scan.remaining(o.Package, o.Advisories)) > 0 {
			continue
		}
		if final, err = removeManifestOverride(final, opath, o.Package); err != nil {
			return nil, err
		}
		r.lock.drop(o.Manifest, o.Package)
		applied = append(applied, AppliedUpdate{
			Dep:         supplychain.Dependency{Name: o.Package, Ecosystem: supplychain.EcosystemNpm, File: p.lockfile, Indirect: true},
			OldVer:      o.Floor,
			NewVer:      strings.Join(scan.copies[o.Package], ", "),
			UpdateType:  "lock",
			Remediation: fmt.Sprintf("removed StageFreight's override floor %s %s: its dependents now resolve past it", o.Package, o.Floor),
		})
	}
	switch {
	case len(applied) == 0:
		if err := os.WriteFile(manifestAbs, origManifest, 0o644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(lockAbs, origLock, 0o644); err != nil {
			return nil, err
		}
	case !bytes.Equal(final, manifest):
		if err := os.WriteFile(manifestAbs, final, 0o644); err != nil {
			return nil, err
		}
		if err := r.sync(ctx, p); err != nil {
			return nil, fmt.Errorf("lockfile sync while restoring override floors: %w", err)
		}
	}
	return applied, nil
}

// overrideHeld returns why an override floor may not be written, or "".
func (r *jsRemediator) overrideHeld(p jsProject, manifest []byte, name, fix string, copies []string) string {
	mode := r.cfg.TransitiveOverride
	if mode == "" {
		mode = "compatible"
	}
	if mode == "off" {
		return "the override lane is off (dependency.transitive.override: off)"
	}
	if p.manager == jsManagerPnpm && pnpmWorkspaceDeclaresOverrides(p.dir) {
		return "this project keeps its overrides in pnpm-workspace.yaml, which StageFreight does not edit"
	}
	if existing, ok := manifestOverride(manifest, p.overridePath(), name); ok {
		return fmt.Sprintf("an existing override sets it to %s, which StageFreight did not write", existing)
	}
	for _, c := range copies {
		switch overrideRelation(c, fix) {
		case relationAbove:
			return fmt.Sprintf("a floor of ^%s would move locked %s off its newer line", fix, c)
		case relationIncompatible:
			if mode == "compatible" {
				return fmt.Sprintf("fix %s is not semver-compatible with locked %s (dependency.transitive.override: any allows it)", fix, c)
			}
		case relationUnparsed:
			return fmt.Sprintf("locked version %s is not semver", c)
		}
	}
	return ""
}

type versionRelation int

const (
	relationCompatible   versionRelation = iota // same line as the fix, at or below it
	relationIncompatible                        // an older line: the floor crosses semver
	relationAbove                               // a newer line: an unscoped floor would move it down
	relationUnparsed
)

// overrideRelation places a locked version relative to the floor ^fix.
func overrideRelation(locked, fix string) versionRelation {
	l, err1 := masterminds.NewVersion(locked)
	f, err2 := masterminds.NewVersion(fix)
	if err1 != nil || err2 != nil {
		return relationUnparsed
	}
	line := func(v *masterminds.Version) [2]uint64 {
		if v.Major() == 0 {
			return [2]uint64{0, v.Minor()}
		}
		return [2]uint64{v.Major(), 0}
	}
	ll, fl := line(l), line(f)
	switch {
	case ll == fl:
		return relationCompatible
	case ll[0] > fl[0] || (ll[0] == fl[0] && ll[1] > fl[1]):
		return relationAbove
	}
	return relationIncompatible
}

// scan reads the lockfile's vulnerable transitive packages and locked versions, with
// the operator's accepted-risk ignores applied.
func (r *jsRemediator) scan(ctx context.Context, p jsProject, lockAbs string, direct map[string]bool) (jsScan, error) {
	obs, locked, err := analysis.InventoryLockfile(ctx, r.scanner, toolchain.CleanEnv(), lockAbs, p.lockfile)
	if err != nil {
		return jsScan{}, err
	}
	out := jsScan{vulns: map[string]*jsVuln{}, copies: map[string][]string{}}
	for _, l := range locked {
		out.copies[l.Name] = appendUnique(out.copies[l.Name], l.Version)
	}
	for name := range out.copies {
		sortVersions(out.copies[name])
	}
	var deps []supplychain.Dependency
	index := map[string]int{}
	for _, o := range obs {
		if direct[o.Package] || !strings.EqualFold(o.Ecosystem, "npm") {
			continue
		}
		i, ok := index[o.Package]
		if !ok {
			i = len(deps)
			index[o.Package] = i
			deps = append(deps, supplychain.Dependency{Name: o.Package, Ecosystem: supplychain.EcosystemNpm, File: p.lockfile, Indirect: true})
		}
		deps[i].Current = o.Version
		deps[i].Vulnerabilities = append(deps[i].Vulnerabilities, supplychain.VulnInfo{ID: o.VulnID, Aliases: o.Aliases, Severity: o.Severity, FixedIn: o.FixedIn, Summary: o.Summary, Source: o.Source})
	}
	deps = ApplyIgnores(deps, r.cfg.Ignore, time.Now())
	vulnVersions := map[string][]string{}
	for _, o := range obs {
		vulnVersions[o.Package] = appendUnique(vulnVersions[o.Package], o.Version)
	}
	for _, d := range deps {
		if len(d.Vulnerabilities) == 0 {
			continue
		}
		v := &jsVuln{versions: vulnVersions[d.Name], advisories: map[string]supplychain.VulnInfo{}}
		sortVersions(v.versions)
		for _, a := range d.Vulnerabilities {
			if prev, ok := v.advisories[a.ID]; !ok || higherVersion(a.FixedIn, prev.FixedIn) {
				v.advisories[a.ID] = a
			}
		}
		out.vulns[d.Name] = v
	}
	return out, nil
}

func (r *jsRemediator) node(_ context.Context) (*nodeToolRunner, error) {
	if r.tools == nil {
		t, err := resolveNodeTools(r.repoRoot)
		if err != nil {
			return nil, err
		}
		r.tools = t
	}
	return r.tools, nil
}

// refresh re-resolves names within every declared range, lockfile only.
func (r *jsRemediator) refresh(ctx context.Context, p jsProject, names []string) error {
	tools, err := r.node(ctx)
	if err != nil {
		return err
	}
	var tool string
	var args, env []string
	switch p.manager {
	case jsManagerNpm:
		tool, args = "npm", hardenedNpmArgs(append(append([]string{"update"}, names...), "--package-lock-only"))
	case jsManagerYarn:
		tool, args, env = "corepack", append(append([]string{"yarn", "up", "-R"}, names...), "--mode=update-lockfile"), []string{"YARN_ENABLE_SCRIPTS=false"}
	case jsManagerPnpm:
		tool, args = "corepack", append(append([]string{"pnpm", "update"}, names...), "--depth", "Infinity", "--lockfile-only", "--ignore-scripts")
	default:
		return fmt.Errorf("%s has no lockfile-only refresh", p.manager)
	}
	if out, err := tools.run(ctx, p.dir, tool, env, args...); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", tool, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sync re-resolves the lockfile after a manifest edit.
func (r *jsRemediator) sync(ctx context.Context, p jsProject) error {
	tools, err := r.node(ctx)
	if err != nil {
		return err
	}
	_, err = tools.syncLock(ctx, r.repoRoot, p.dir)
	return err
}

// manifestDependencyNames returns every package the manifest declares directly; those
// belong to the ordinary update pass.
func manifestDependencyNames(manifest []byte) map[string]bool {
	var m struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
	}
	_ = json.Unmarshal(manifest, &m)
	out := map[string]bool{}
	for _, deps := range []map[string]string{m.Dependencies, m.DevDependencies, m.OptionalDependencies, m.PeerDependencies} {
		for name := range deps {
			out[name] = true
		}
	}
	return out
}

func pnpmWorkspaceDeclaresOverrides(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "pnpm-workspace.yaml"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "overrides:") {
			return true
		}
	}
	return false
}

func requiredBy(lockAbs, manager, name string) string {
	reqs := jsRequirers(lockAbs, manager, name)
	if len(reqs) == 0 {
		return ""
	}
	const max = 3
	if len(reqs) > max {
		reqs = append(reqs[:max], fmt.Sprintf("%d more", len(reqs)-max))
	}
	return " — " + strings.Join(reqs, "; ")
}

func jsDep(p jsProject, name string, v *jsVuln) supplychain.Dependency {
	d := supplychain.Dependency{Name: name, Ecosystem: supplychain.EcosystemNpm, File: p.lockfile, Indirect: true}
	if v != nil {
		d.Current = strings.Join(v.versions, ", ")
		for _, id := range sortedAdvisoryIDs(v) {
			d.Vulnerabilities = append(d.Vulnerabilities, v.advisories[id])
		}
	}
	return d
}

func jsApplied(p jsProject, name string, before, after jsScan, ids []string, how string) AppliedUpdate {
	return AppliedUpdate{
		Dep:         jsDep(p, name, before.vulns[name]),
		OldVer:      strings.Join(before.vulns[name].versions, ", "),
		NewVer:      strings.Join(after.copies[name], ", "),
		UpdateType:  "security",
		CVEsFixed:   ids,
		Remediation: how,
	}
}

// highestFix is the floor that satisfies every one of ids.
func highestFix(v *jsVuln, ids []string) string {
	fix := ""
	for _, id := range ids {
		if f := v.advisories[id].FixedIn; higherVersion(f, fix) {
			fix = f
		}
	}
	return fix
}

func higherVersion(a, b string) bool {
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	va, e1 := masterminds.NewVersion(a)
	vb, e2 := masterminds.NewVersion(b)
	if e1 != nil || e2 != nil {
		return a > b
	}
	return va.GreaterThan(vb)
}

func sortVersions(vs []string) {
	sort.Slice(vs, func(i, j int) bool { return higherVersion(vs[j], vs[i]) })
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func sortedVulnNames(s jsScan) []string {
	names := make([]string, 0, len(s.vulns))
	for n := range s.vulns {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedAdvisoryIDs(v *jsVuln) []string {
	ids := make([]string, 0, len(v.advisories))
	for id := range v.advisories {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedFloorNames(m map[string]overrideFloor) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
