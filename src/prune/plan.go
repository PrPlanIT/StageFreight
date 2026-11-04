// Package prune is StageFreight's disk-GC lifecycle: it gives every SF-created
// artifact a default retention lifecycle so a CI runner never fills its disk from SF
// residue, while NEVER touching anything StageFreight cannot positively claim.
//
// The invariant the whole package is built around:
//
//	StageFreight never needs to guess whether something belongs to someone else
//	in order to reclaim space.
//
// Ownership is established at the ARTIFACT level, never inferred from the daemon or
// host containing it. Three classes:
//
//	namespace  — inside the SF cache root (cache/ + toolchains/): inherently SF-owned.
//	            Every object there participates in a default lifecycle even when its
//	            subsystem has no specialized policy (the backstop).
//	provenance — SF-created artifacts identified positively (the repo's own published
//	            image streams, the sf-builder's cache via its own API).
//	declared   — third-party artifacts the OPERATOR adopted by declaration
//	            (build_cache.cleanup.prune.images.refs) — an authorization boundary,
//	            never a confidence boundary. Requires cleanup enabled/--host-cleanup.
//
// Anything not in those classes is left alone, in every daemon, always.
package prune

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Class is the ownership class that made an action eligible.
type Class string

const (
	ClassNamespace  Class = "namespace"  // SF cache root — inherently owned
	ClassProvenance Class = "provenance" // positively SF-created
	ClassDeclared   Class = "declared"   // operator-adopted via cleanup declaration
)

// Kind selects the executor for an action.
type Kind string

const (
	KindUnits          Kind = "units"           // cache units unused beyond their age, evicted whole
	KindPressure       Kind = "pressure"        // least recently used cache units, down to the low watermark
	KindGoBuildCache   Kind = "go-build-cache"  // per-entry LRU of the Go build cache (GOCACHE)
	KindLintCache      Kind = "lint-cache"      // per-entry LRU of the lint result cache
	KindToolchains     Kind = "toolchains"      // keep-N toolchain versions (policy grammar)
	KindImageStream    Kind = "image-stream"    // SF's own published generations, local daemon
	KindBuildkit       Kind = "buildkit"        // sf-builder cache via `docker buildx prune`
	KindDeclaredImages Kind = "declared-images" // operator-declared third-party eviction
	KindHostResidue    Kind = "host-residue"    // dangling layers + exited containers (authorized)
	KindDaemonImages   Kind = "daemon-images"   // runner-declared image GC on the job's Docker daemon
)

// Action is one planned reclaim. The planner only plans — Execute mutates.
type Action struct {
	Kind   Kind
	Class  Class
	Label  string // human label ("cache/go/build", "toolchains", …)
	Path   string // filesystem dir (cache subsystem / toolchains root)
	Reason string

	// LockRoot is the cache root a namespace action evicts from; Execute takes it
	// exclusively when no other run holds it (paths.ExclusiveCacheRoot).
	LockRoot string

	MaxAge  time.Duration            // entries / units unused longer are evicted
	MaxSize int64                    // per-entry LRU: then least recently used down to this
	Ages    map[string]time.Duration // units: max age by unit path relative to Path (default MaxAge)
	Low     float64                  // pressure: evict until the filesystem is at most this full
	MinAge  time.Duration            // daemon images: never evict one pulled more recently
	Invalid string                   // the declaration couldn't be parsed; the action reports it and does nothing
	Policy  config.RetentionPolicy   // toolchains / image-stream
	Pins    map[string]string        // toolchains: tool → pinned constraint (protected)

	Streams   []string              // image-stream: repo path suffixes (slugs) owned by this config
	Templates []string              // image-stream: the publish targets' tag templates (candidacy)
	Refs      []config.RetentionRef // declared-images: the operator's adoption list
	Builder   string                // buildkit: builder name
	KeepStore string                // buildkit: --keep-storage
	Until     string                // buildkit / host-residue: --filter until=
}

// Options steers a planning pass.
type Options struct {
	CacheRoot   string  // SF cache mount root ("" = discover via caller)
	Target      float64 // pressure gate: plan only when used-fraction ≥ Target (0 = always plan)
	HostCleanup bool    // authorization: include declared/host actions (cleanup config or --host-cleanup)
}

// Engine defaults — behavior, not policy (deliberately NOT config; see the plan's
// "Rejected shapes"). Visible in every dry-run and the perform Disk GC box.
const (
	DefaultTarget       = 0.80                // pressure gate: the high watermark
	pressureHysteresis  = 0.05                // the low watermark sits this far below it
	goBuildMaxSize      = 5 << 30             // cache/go/build LRU cap
	backstopMaxAge      = 30 * 24 * time.Hour // any cache subsystem without a named policy
	defaultLintAge      = 7 * 24 * time.Hour
	defaultLintSize     = 100 << 20
	defaultBuildkitAge  = 7 * 24 * time.Hour
	imageStreamKeepLast = 3 // SF image generations when the target declares no retention
)

// UsedFraction reports the used fraction of the filesystem containing path.
func UsedFraction(path string) float64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Blocks == 0 {
		return 0
	}
	total := float64(st.Blocks) * float64(st.Bsize)
	free := float64(st.Bavail) * float64(st.Bsize)
	return 1 - free/total
}

// Plan computes the reclaim actions the ownership model + declared policy allow.
// cfg may be nil (running on a bare runner host with no .stagefreight.yml): the
// SF-owned namespace lifecycle still applies with engine defaults; provenance-class
// image streams and declared adoptions need the config that declares them.
// Pure planning — nothing is mutated.
func Plan(cfg *config.Config, opts Options) []Action {
	// The daemon's image budget has its own trigger: it runs whatever the cache root's
	// pressure.
	actions := daemonImageAction()
	if opts.Target > 0 && opts.CacheRoot != "" && UsedFraction(opts.CacheRoot) < opts.Target {
		return actions // healthy — the pressure gate keeps the rest a cheap no-op
	}

	// ── class 1: the SF cache-root namespace ──────────────────────────────────
	if opts.CacheRoot != "" {
		cacheDir := filepath.Join(opts.CacheRoot, "cache")
		if _, err := os.Stat(filepath.Join(cacheDir, "go", "build")); err == nil {
			actions = append(actions, Action{Kind: KindGoBuildCache, Class: ClassNamespace, Label: "cache/go/build",
				Path: filepath.Join(cacheDir, "go", "build"), MaxSize: goBuildMaxSize,
				Reason: "go build cache over size cap (rebuildable)"})
		}
		if _, err := os.Stat(filepath.Join(cacheDir, "lint")); err == nil {
			age, size := defaultLintAge, int64(defaultLintSize)
			if cfg != nil {
				if d, err := config.ParseDuration(cfg.Lint.Cache.MaxAge); err == nil && cfg.Lint.Cache.MaxAge != "" {
					age = d
				}
				if s, err := config.ParseSize(cfg.Lint.Cache.MaxSize); err == nil && cfg.Lint.Cache.MaxSize != "" {
					size = s
				}
			}
			actions = append(actions, Action{Kind: KindLintCache, Class: ClassNamespace,
				Label: "cache/lint", Path: filepath.Join(cacheDir, "lint"), MaxAge: age, MaxSize: size,
				Reason: "lint results beyond lint.cache retention"})
		}
		if _, err := os.Stat(cacheDir); err == nil {
			ages := map[string]time.Duration{}
			if cfg != nil {
				if d, err := config.ParseDuration(cfg.BuildCache.Local.Retention.MaxAge); err == nil && d > 0 {
					ages["buildkit"] = d
				}
			} else {
				ages["buildkit"] = defaultBuildkitAge
			}
			actions = append(actions, Action{Kind: KindUnits, Class: ClassNamespace, Label: "cache units",
				Path: cacheDir, MaxAge: backstopMaxAge, Ages: ages,
				Reason: "cache units unused beyond their age (backstop 30d)"})
		}

		// Toolchain versions: the declared toolchains.retention grammar (scoped refs,
		// protect, max_age) with engine default keep_last=2; pins always protected.
		tcRoot := filepath.Join(opts.CacheRoot, "toolchains")
		if _, err := os.Stat(tcRoot); err == nil {
			a := Action{Kind: KindToolchains, Class: ClassNamespace, Label: "toolchains",
				Path: tcRoot, Reason: "versions beyond toolchains.retention (pinned protected)"}
			if cfg != nil {
				a.Policy = cfg.Toolchains.Retention
				a.Pins = map[string]string{}
				for tool, pin := range cfg.Toolchains.Want {
					if pin.Constraint != "" {
						a.Pins[tool] = pin.Constraint
					}
				}
			}
			actions = append(actions, a)
		}
		if opts.Target > 0 {
			actions = append(actions, Action{Kind: KindPressure, Class: ClassNamespace, Label: "disk pressure",
				Path: cacheDir, Low: opts.Target - pressureHysteresis,
				Reason: "least recently used cache units while the disk is over target"})
		}
		for i := range actions {
			if actions[i].Class == ClassNamespace {
				actions[i].LockRoot = opts.CacheRoot
			}
		}
	}

	// ── class 2: positive provenance ──────────────────────────────────────────
	if cfg != nil {
		// SF's own image generations on the local daemon: candidacy comes from the
		// repo's OWN publish-target tag templates (the same declaration that governs
		// the registry), policy from the target's retention. One stream, two locations.
		if slug := primarySlug(cfg); slug != "" {
			var templates []string
			policy := config.RetentionPolicy{}
			for _, t := range cfg.Targets {
				if t.Kind != "registry" || t.Retention == nil || !t.Retention.Active() {
					continue
				}
				templates = append(templates, t.Tags...)
				if t.Retention.KeepLast > policy.KeepLast {
					policy = *t.Retention
				}
			}
			if len(templates) > 0 {
				if !policy.Active() {
					policy.KeepLast = imageStreamKeepLast
				}
				actions = append(actions, Action{Kind: KindImageStream, Class: ClassProvenance,
					Label:   "image generations (" + slug + ")",
					Streams: []string{slug}, Templates: templates, Policy: policy,
					Reason: "superseded local generations of this repo's published stream"})
			}
		}
		// The sf-builder's cache, managed THROUGH the builder API — never a volume rm.
		if cfg.BuildCache.IsActive() {
			ret := cfg.BuildCache.Local.Retention
			a := Action{Kind: KindBuildkit, Class: ClassProvenance, Label: "sf-builder cache",
				Builder: cfg.BuildCache.Builder.BuilderName(),
				Reason:  "buildkit cache beyond build_cache.local.retention (via buildx prune)"}
			if ret.MaxSize != "" {
				a.KeepStore = ret.MaxSize
			}
			if ret.MaxAge != "" {
				a.Until = ret.MaxAge
			}
			actions = append(actions, a)
		}
	}

	// ── class 3: operator-declared adoption (authorization boundary) ──────────
	if opts.HostCleanup && cfg != nil {
		cl := cfg.BuildCache.Cleanup
		if len(cl.Prune.Images.Refs) > 0 {
			actions = append(actions, Action{Kind: KindDeclaredImages, Class: ClassDeclared,
				Label: "declared image eviction", Refs: cl.Prune.Images.Refs,
				Reason: "operator-declared third-party streams (cleanup.prune.images.refs)"})
		}
		if until := cl.Prune.Images.Dangling.OlderThan; until != "" {
			actions = append(actions, Action{Kind: KindHostResidue, Class: ClassDeclared,
				Label: "dangling layers + exited containers", Until: until,
				Reason: "authorized host residue (cleanup.prune)"})
		}
	}

	return actions
}

// primarySlug derives the repo slug from the primary repo's project path; a templated
// project ("{var:...}") cannot anchor a slug and yields "".
func primarySlug(cfg *config.Config) string {
	for _, r := range cfg.Repos {
		for _, role := range r.Roles {
			if role == "primary" {
				p := r.Project
				if strings.Contains(p, "{") {
					return ""
				}
				if i := strings.LastIndexByte(p, '/'); i >= 0 {
					p = p[i+1:]
				}
				return strings.ToLower(p)
			}
		}
	}
	return ""
}

// FreeBytes reports the filesystem's available bytes at path.
func FreeBytes(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
