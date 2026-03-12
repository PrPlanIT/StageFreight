package lint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"golang.org/x/sync/semaphore"
)

// Engine orchestrates lint modules across files.
type Engine struct {
	Config  config.LintConfig
	RootDir string
	Modules []Module
	Cache   *Cache
	Verbose bool

	CacheHits   atomic.Int64
	CacheMisses atomic.Int64
}

// NewEngine creates a lint engine with the selected modules.
func NewEngine(cfg config.LintConfig, rootDir string, moduleNames []string, skipNames []string, verbose bool, cache *Cache) (*Engine, error) {
	skipSet := make(map[string]bool, len(skipNames))
	for _, name := range skipNames {
		skipSet[name] = true
	}

	var modules []Module

	if len(moduleNames) > 0 {
		// Explicit module selection
		for _, name := range moduleNames {
			if skipSet[name] {
				continue
			}
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

			// Check config for explicit enable/disable override
			mc, hasCfg := cfg.Modules[name]
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

		for mi, mod := range e.Modules {
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

	if len(errs) > 0 {
		return findings, modStats, fmt.Errorf("%d module errors (first: %w)", len(errs), errs[0])
	}

	return findings, modStats, nil
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

// configureModule passes YAML options to modules that implement ConfigurableModule.
func configureModule(m Module, cfg config.LintConfig, name string) error {
	cm, ok := m.(ConfigurableModule)
	if !ok {
		return nil
	}
	mc, exists := cfg.Modules[name]
	if !exists || mc.Options == nil {
		// Call with empty map so the module can apply defaults.
		return cm.Configure(nil)
	}
	return cm.Configure(mc.Options)
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
