package lint

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"golang.org/x/sync/semaphore"
)

// Engine orchestrates lint modules across files.
type Engine struct {
	Config           config.LintConfig
	RootDir          string
	Modules          []Module
	Cache            *Cache
	Verbose          bool
	ToolchainDesired map[string]config.ToolPinConfig

	// Snapshot is an optional, pre-resolved supply-chain Snapshot produced
	// once (via discovery.Discover) and shared by the caller across
	// consumers — e.g. the audition pipeline threads the same Snapshot into
	// both the lint pass and the dependency-update step so resolution runs
	// once instead of per-consumer. Set by the caller before RunWithStats,
	// mirroring ToolchainDesired. nil means "no shared Snapshot" — modules
	// implementing SnapshotAwareModule must fall back to on-demand
	// resolution (this is what keeps standalone `stagefreight lint` working).
	Snapshot *supplychain.Snapshot

	CacheHits   atomic.Int64
	CacheMisses atomic.Int64

	// BinariesScanned counts files classified non-text this run — the coverage
	// roll-up ("N binaries scanned"). Inspected ≠ emitted: a clean binary increments
	// this but produces no finding.
	BinariesScanned atomic.Int64

	// ClassifyUnreadable counts files whose bytes couldn't be read for classification
	// (permissions, IO, races). They fail OPEN — treated as text so checks still run —
	// but the count is surfaced so environmental breakage isn't silently swallowed.
	ClassifyUnreadable atomic.Int64

	// NonText is the disclosure inventory: every non-text artifact this run, for the
	// ungraded "validate these are deliberate" review surface (NOT findings). Populated
	// in the sequential classification pass, so a plain slice is safe.
	NonText []NonTextEntry

	// NonAuthored is the provenance disclosure: generated/vendored/lockfile files this
	// run. Authored-code hygiene was relaxed for them, but they stay visible — and every
	// security/supply-chain module still ran. Populated in the sequential pass.
	NonAuthored []ProvenanceEntry

	// allPaths is the full set of repo-relative paths from CollectFiles, retained so
	// provenance vendor-root derivation reflects the whole repo even when RunWithStats is
	// given a delta-filtered subset. Empty when files are supplied without CollectFiles
	// (e.g. the baseline base-relint), where the scanned set is used instead.
	allPaths []string
}

// NonTextEntry is one non-text artifact for the disclosure inventory — a review
// surface, never a graded finding.
type NonTextEntry struct {
	Path string
	Type string // magic label, or "ambiguous" / "binary"
}

// ProvenanceEntry is one non-authored file for the provenance disclosure roll-up.
type ProvenanceEntry struct {
	Path   string
	Kind   string // "generated" | "vendored" | "lockfile"
	Source string // evidence: "config", "marker", "lockfile:Cargo.lock", …
}

// contentTypeLabel is the human label shown in the disclosure inventory.
func contentTypeLabel(c Content) string {
	if c.Magic != "" {
		return c.Magic
	}
	if c.Kind == ContentAmbiguous {
		return "ambiguous"
	}
	return "binary"
}

// NewEngine creates a lint engine with the selected modules.
func NewEngine(cfg config.LintConfig, rootDir string, moduleNames []string, skipNames []string, verbose bool, cache *Cache) (*Engine, error) {
	skipSet := make(map[string]bool, len(skipNames))
	for _, name := range skipNames {
		skipSet[canonicalModuleName(name)] = true
	}

	var modules []Module

	if len(moduleNames) > 0 {
		// Explicit module selection. Resolve deprecated aliases (e.g. "osv" →
		// "vulnerabilities") to their canonical registered name and de-duplicate,
		// so `--module osv` and an explicit list containing "osv" both work.
		seen := make(map[string]bool, len(moduleNames))
		for _, raw := range moduleNames {
			name := canonicalModuleName(raw)
			if skipSet[name] || seen[name] {
				continue
			}
			seen[name] = true
			m, err := Get(name)
			if err != nil {
				return nil, err
			}
			if err := configureModule(m, cfg, name); err != nil {
				return nil, err
			}
			modules = append(modules, m)
		}
	} else {
		// All default-enabled modules minus skipped
		for _, name := range All() {
			if skipSet[name] {
				continue
			}
			m, err := Get(name)
			if err != nil {
				return nil, err
			}

			// Check config for explicit enable/disable override, honoring
			// deprecated alias keys (e.g. lint.modules.osv still configures the
			// vulnerabilities module).
			mc, hasCfg := moduleConfigFor(cfg, name)
			if hasCfg && mc.Enabled != nil {
				if !*mc.Enabled {
					continue // explicitly disabled
				}
				// explicitly enabled — include regardless of DefaultEnabled
			} else if !m.DefaultEnabled() {
				continue // not configured and not default-enabled
			}

			if err := configureModule(m, cfg, name); err != nil {
				return nil, err
			}
			modules = append(modules, m)
		}
	}

	if len(modules) == 0 {
		return nil, fmt.Errorf("no lint modules selected")
	}

	// Note: ToolchainDesired is set by the caller after construction,
	// before Run(). Modules that implement ToolchainAwareModule receive
	// it via applyToolchainDesired() called from Run().

	return &Engine{
		Config:  cfg,
		RootDir: rootDir,
		Modules: modules,
		Cache:   cache,
		Verbose: verbose,
	}, nil
}

// ModuleStats holds per-module scan statistics.
type ModuleStats struct {
	Name     string
	Files    int
	Cached   int
	Findings int
	Critical int
	Warnings int
}

// Run executes all modules against the given files and returns findings.
func (e *Engine) Run(ctx context.Context, files []FileInfo) ([]Finding, error) {
	findings, _, err := e.RunWithStats(ctx, files)
	return findings, err
}

// RunWithStats executes all modules and returns findings plus per-module statistics.
func (e *Engine) RunWithStats(ctx context.Context, files []FileInfo) ([]Finding, []ModuleStats, error) {
	// Propagate toolchain config to modules that need it.
	if e.ToolchainDesired != nil {
		for _, m := range e.Modules {
			if ta, ok := m.(ToolchainAwareModule); ok {
				ta.SetToolchainDesired(e.ToolchainDesired)
			}
		}
	}

	// Propagate a pre-resolved Snapshot to modules that need it. Modules
	// implementing SnapshotAwareModule use this to skip on-demand resolution
	// entirely when the caller already resolved once (the audition path);
	// when Snapshot is nil (standalone `stagefreight lint`), SetSnapshot is
	// never called and modules fall back to resolving per-file.
	if e.Snapshot != nil {
		for _, m := range e.Modules {
			if sa, ok := m.(SnapshotAwareModule); ok {
				sa.SetSnapshot(e.Snapshot)
			}
		}
	}

	e.BinariesScanned.Store(0) // reset per run; populated in the classification pass
	e.ClassifyUnreadable.Store(0)
	e.NonText = nil
	e.NonAuthored = nil

	// Provenance needs a whole-set pre-pass: a vendor marker in one file marks its entire
	// directory vendored, so every file beneath inherits it. Crucially this must see the
	// WHOLE repo, not just the scanned subset — under --level changed a vendor marker
	// (.cargo_vcs_info.json) is rarely in the changeset, yet a changed file beneath it is
	// still vendored. Use the full collected set when present; fall back to the scan set.
	rootPaths := make([]string, len(files))
	for i, f := range files {
		rootPaths[i] = f.Path
	}
	if len(e.allPaths) > 0 {
		rootPaths = e.allPaths
	}
	vendoredRoots := deriveVendoredRoots(rootPaths)

	var (
		mu       sync.Mutex
		findings []Finding
		wg       sync.WaitGroup
		errs     []error
	)

	sem := semaphore.NewWeighted(int64(runtime.NumCPU() * 2))

	// Per-module stat counters (index matches e.Modules)
	modStats := make([]ModuleStats, len(e.Modules))
	for i, m := range e.Modules {
		modStats[i].Name = m.Name()
	}

	// Partition modules by dispatch shape. Per-file modules fan out one Check per
	// (file, module) below; whole-repo modules run ONCE over the whole eligible
	// set after the per-file pass (see WholeRepoModule). Original indices are kept
	// so both paths write the same modStats slots.
	type indexedModule struct {
		idx int
		mod Module
	}
	var perFile, wholeRepo []indexedModule
	for mi, mod := range e.Modules {
		if _, ok := mod.(WholeRepoModule); ok {
			wholeRepo = append(wholeRepo, indexedModule{mi, mod})
		} else {
			perFile = append(perFile, indexedModule{mi, mod})
		}
	}

	// classified retains every file that survived engine-wide exclusion, WITH its
	// content/provenance classification applied, so whole-repo modules receive the
	// same enriched FileInfo the per-file pass saw — computed once.
	var classified []FileInfo

	for _, file := range files {
		if e.isExcluded(file.Path) {
			continue
		}

		// Read file content once for cache keying
		var content []byte
		if e.Cache != nil && e.Cache.Enabled {
			var err error
			content, err = os.ReadFile(file.AbsPath)
			if err != nil {
				// Non-fatal — run without cache for this file
				content = nil
			}
		}

		// Classify content once, centrally: text modules route on file.Content; the
		// binary count feeds the coverage roll-up. Reuse the cache read if present,
		// else a cheap prefix.
		classData := content
		if classData == nil {
			classData = readPrefix(file.AbsPath, classifyPrefix)
		}
		if classData == nil && file.Size > 0 {
			// Non-empty file we couldn't read to classify: fail OPEN (→ text, so checks
			// still run) but record it so environmental breakage stays visible.
			e.ClassifyUnreadable.Add(1)
		}
		file.Content = classifyContent(classData)
		if !file.Content.IsText() {
			e.BinariesScanned.Add(1)
			e.NonText = append(e.NonText, NonTextEntry{Path: file.Path, Type: contentTypeLabel(file.Content)})
		}

		// Classify provenance (fail-closed to authored). Hygiene modules route on it;
		// security/supply-chain modules ignore it. Non-authored files are disclosed.
		file.Provenance = classifyProvenance(file.Path, classData, e.Config.Provenance, vendoredRoots)
		if !file.Provenance.IsAuthored() {
			e.NonAuthored = append(e.NonAuthored, ProvenanceEntry{
				Path: file.Path, Kind: file.Provenance.Kind.String(), Source: file.Provenance.Source,
			})
		}

		// Retain the classified file (value copy) for the whole-repo pass.
		classified = append(classified, file)

		for _, im := range perFile {
			mod, mi := im.mod, im.idx
			wg.Add(1)
			sem.Acquire(ctx, 1)
			go func(m Module, f FileInfo, data []byte, idx int) {
				defer wg.Done()
				defer sem.Release(1)

				// Per-module file exclusion
				if e.isModuleExcluded(m.Name(), f.Path) {
					return
				}

				// Check cache
				if e.Cache != nil && e.Cache.Enabled && data != nil {
					cfgJSON := e.moduleConfigJSON(m.Name())
					key := e.Cache.Key(data, m.Name(), cfgJSON)

					// Resolve cache TTL: modules with external state
					// declare a TTL; all others cache forever (maxAge=0).
					var maxAge time.Duration
					noCache := false
					if tm, ok := m.(CacheTTLModule); ok {
						maxAge = tm.CacheTTL()
						if maxAge < 0 {
							noCache = true // negative = never cache
							maxAge = 0
						}
					}

					if !noCache {
						if cached, ok := e.Cache.Get(key, maxAge); ok {
							e.CacheHits.Add(1)
							mu.Lock()
							modStats[idx].Files++
							modStats[idx].Cached++
							for _, f := range cached {
								modStats[idx].Findings++
								if f.Severity == SeverityCritical {
									modStats[idx].Critical++
								} else if f.Severity == SeverityWarning {
									modStats[idx].Warnings++
								}
							}
							findings = append(findings, cached...)
							mu.Unlock()
							return
						}
					}
					e.CacheMisses.Add(1)

					// Run module and cache result
					results, err := m.Check(ctx, f)
					mu.Lock()
					defer mu.Unlock()
					modStats[idx].Files++
					if err != nil {
						errs = append(errs, fmt.Errorf("%s: %s: %w", m.Name(), f.Path, err))
						return
					}
					for _, r := range results {
						modStats[idx].Findings++
						if r.Severity == SeverityCritical {
							modStats[idx].Critical++
						} else if r.Severity == SeverityWarning {
							modStats[idx].Warnings++
						}
					}
					findings = append(findings, results...)
					// Cache even empty results (clean pass).
					// Skip write for modules that opted out (TTL<0).
					if !noCache {
						if cacheErr := e.Cache.Put(key, results); cacheErr != nil {
							diag.Debug(e.Verbose, "cache: write failed for %s/%s: %v", m.Name(), f.Path, cacheErr)
						}
					}
					return
				}

				// No cache — run directly
				results, err := m.Check(ctx, f)
				mu.Lock()
				defer mu.Unlock()
				modStats[idx].Files++
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %s: %w", m.Name(), f.Path, err))
					return
				}
				for _, r := range results {
					modStats[idx].Findings++
					if r.Severity == SeverityCritical {
						modStats[idx].Critical++
					} else if r.Severity == SeverityWarning {
						modStats[idx].Warnings++
					}
				}
				findings = append(findings, results...)
			}(mod, file, content, mi)
		}
	}

	wg.Wait()

	// Whole-repo pass: each whole-repo module runs ONCE over its eligible file
	// subset (engine-wide exclusion already applied via `classified`; this
	// module's own excludes applied here). Sequential and after the per-file
	// barrier, so appends to findings/modStats need no locking. Whole-repo
	// modules bypass the per-file content-hash cache — their result is a function
	// of the WHOLE set, which that cache does not model.
	for _, im := range wholeRepo {
		wr := im.mod.(WholeRepoModule)

		subset := make([]FileInfo, 0, len(classified))
		for _, f := range classified {
			if e.isModuleExcluded(im.mod.Name(), f.Path) {
				continue
			}
			subset = append(subset, f)
		}

		modStats[im.idx].Files = len(subset)
		results, err := wr.CheckAll(ctx, subset)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", im.mod.Name(), err))
			continue
		}
		for _, r := range results {
			modStats[im.idx].Findings++
			if r.Severity == SeverityCritical {
				modStats[im.idx].Critical++
			} else if r.Severity == SeverityWarning {
				modStats[im.idx].Warnings++
			}
		}
		findings = append(findings, results...)
	}

	if len(errs) > 0 {
		return findings, modStats, fmt.Errorf("%d module errors (first: %w)", len(errs), errs[0])
	}

	return findings, modStats, nil
}

// readPrefix reads up to n bytes from the head of a file for content classification —
// cheap and enough for magic/encoding heuristics. Returns nil (→ treated as text) when
// unreadable, so an unreadable file is never silently misrouted as binary.
func readPrefix(path string, n int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, n)
	got, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil
	}
	return buf[:got]
}

// CollectFiles walks the root directory and returns FileInfo for all regular files.
func (e *Engine) CollectFiles() ([]FileInfo, error) {
	var files []FileInfo

	err := filepath.WalkDir(e.RootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(e.RootDir, path)
		if err != nil {
			return err
		}

		// Skip hidden directories and .git
		if d.IsDir() {
			base := filepath.Base(rel)
			if strings.HasPrefix(base, ".") && base != "." {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip non-regular files
		if !d.Type().IsRegular() {
			return nil
		}

		if e.isExcluded(rel) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		files = append(files, FileInfo{
			Path:    rel,
			AbsPath: path,
			Size:    info.Size(),
		})
		return nil
	})

	// Record the full collected set so provenance vendor-root derivation sees the whole
	// repo even when the scan is later delta-filtered to changed files (a vendor marker
	// rarely lives in a changeset).
	e.allPaths = make([]string, len(files))
	for i, f := range files {
		e.allPaths[i] = f.Path
	}
	return files, err
}

// ModuleNames returns the names of all active modules in this engine.
func (e *Engine) ModuleNames() []string {
	names := make([]string, len(e.Modules))
	for i, m := range e.Modules {
		names[i] = m.Name()
	}
	return names
}

// normalizeSlashPath converts a path to forward slashes and strips leading "./".
func normalizeSlashPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return p
}

// matchExcludePattern matches a single exclude pattern against a normalized path.
// Patterns containing "/" or "**" match against the full path; others match base name only.
func matchExcludePattern(pattern, normPath, baseName string) bool {
	pattern = filepath.ToSlash(pattern)
	if strings.Contains(pattern, "/") || strings.Contains(pattern, "**") {
		return matchGlob(pattern, normPath)
	}
	return matchGlob(pattern, baseName)
}

func (e *Engine) isExcluded(path string) bool {
	if len(e.Config.Exclude) == 0 {
		return false
	}
	normPath := normalizeSlashPath(path)
	baseName := filepath.Base(normPath)
	for _, pattern := range e.Config.Exclude {
		if matchExcludePattern(pattern, normPath, baseName) {
			return true
		}
	}
	return false
}

// isModuleExcluded checks per-module exclude patterns from config.
// Engine-wide isExcluded prevents files from being queued at all;
// module excludes prevent only that module from running on matching files.
func (e *Engine) isModuleExcluded(moduleName, path string) bool {
	mc, ok := e.Config.Modules[moduleName]
	if !ok || len(mc.Exclude) == 0 {
		return false
	}
	normPath := normalizeSlashPath(path)
	baseName := filepath.Base(normPath)
	for _, pattern := range mc.Exclude {
		if matchExcludePattern(pattern, normPath, baseName) {
			return true
		}
	}
	return false
}

// moduleAliases maps deprecated/alternate module names to their canonical
// registered name, so config and CLI selection using the old name keep working
// after a rename. The osv module was unified into vulnerabilities.
var moduleAliases = map[string]string{
	"osv": "vulnerabilities",
}

// canonicalModuleName resolves a possibly-deprecated module name to its
// registered name.
func canonicalModuleName(name string) string {
	if canon, ok := moduleAliases[name]; ok {
		return canon
	}
	return name
}

// moduleConfigFor returns the ModuleConfig for a canonical module name, honoring
// deprecated alias keys (e.g. lint.modules.osv still enables/disables the
// vulnerabilities module). The canonical key wins when both are present.
func moduleConfigFor(cfg config.LintConfig, name string) (config.ModuleConfig, bool) {
	if mc, ok := cfg.Modules[name]; ok {
		return mc, true
	}
	for alias, canon := range moduleAliases {
		if canon == name {
			if mc, ok := cfg.Modules[alias]; ok {
				return mc, true
			}
		}
	}
	return config.ModuleConfig{}, false
}

// moduleOptions returns the YAML options a module should be configured with. The
// vulnerabilities module renders the OSV-API correlation that shares config with
// freshness (min_severity, vulnerability toggle, ignores, source toggles), so by
// default it sources its options from the freshness section — keeping both
// modules on the same vulnerability config. But options placed under the
// module's own canonical key (lint.modules.vulnerabilities.options, or the
// deprecated "osv" alias) take precedence when present, so a project that wants
// vulnerabilities configured independently of freshness isn't silently ignored.
func moduleOptions(cfg config.LintConfig, name string) map[string]any {
	if name == "vulnerabilities" {
		if mc, ok := moduleConfigFor(cfg, name); ok && mc.Options != nil {
			return mc.Options
		}
		if mc, ok := cfg.Modules["freshness"]; ok {
			return mc.Options
		}
		return nil
	}
	if mc, ok := moduleConfigFor(cfg, name); ok {
		return mc.Options
	}
	return nil
}

// configureModule passes YAML options to modules that implement ConfigurableModule.
func configureModule(m Module, cfg config.LintConfig, name string) error {
	cm, ok := m.(ConfigurableModule)
	if !ok {
		return nil
	}
	// Call with a nil map (module applies defaults) when no options are present.
	return cm.Configure(moduleOptions(cfg, name))
}

func (e *Engine) moduleConfigJSON(name string) string {
	mc, ok := e.Config.Modules[name]
	if !ok || mc.Options == nil {
		return "{}"
	}
	data, err := json.Marshal(mc.Options)
	if err != nil {
		return "{}"
	}
	return string(data)
}
