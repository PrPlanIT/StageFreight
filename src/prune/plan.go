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
	KindEvictDir       Kind = "evict-dir"       // age→size eviction of a cache subsystem dir
	KindToolchains     Kind = "toolchains"      // keep-N toolchain versions (policy grammar)
	KindImageStream    Kind = "image-stream"    // SF's own published generations, local daemon
	KindBuildkit       Kind = "buildkit"        // sf-builder cache via `docker buildx prune`
	KindDeclaredImages Kind = "declared-images" // operator-declared third-party eviction
	KindHostResidue    Kind = "host-residue"    // dangling layers + exited containers (authorized)
)

// Action is one planned reclaim. The planner only plans — Execute mutates.
type Action struct {
	Kind   Kind
	Class  Class
	Label  string // human label ("cache/go/build", "toolchains", …)
	Path   string // filesystem dir (evict-dir / toolchains root)
	Reason string

	MaxAge  time.Duration          // evict-dir: entries older are evicted
	MaxSize int64                  // evict-dir: then oldest-first down to this
	Policy  config.RetentionPolicy // toolchains / image-stream
	Pins    map[string]string      // toolchains: tool → pinned constraint (protected)

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
	DefaultTarget       = 0.80                // pressure gate
	goBuildMaxSize      = 5 << 30             // cache/go/build LRU cap
	backstopMaxAge      = 30 * 24 * time.Hour // any cache subsystem without a named policy
	defaultLintAge      = 7 * 24 * time.Hour
	defaultLintSize     = 100 << 20
	defaultBuildkitAge  = 7 * 24 * time.Hour
	defaultBuildkitSize = int64(15) << 30
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
	if opts.Target > 0 && opts.CacheRoot != "" && UsedFraction(opts.CacheRoot) < opts.Target {
		return nil // healthy — the pressure gate keeps this a cheap no-op
	}

	var actions []Action

	// ── class 1: the SF cache-root namespace ──────────────────────────────────
	if opts.CacheRoot != "" {
		cacheDir := filepath.Join(opts.CacheRoot, "cache")
		if ents, err := os.ReadDir(cacheDir); err == nil {
			for _, e := range ents {
				if !e.IsDir() {
					continue
				}
				sub := e.Name()
				dir := filepath.Join(cacheDir, sub)
				switch sub {
				case "go":
					actions = append(actions,
						Action{Kind: KindEvictDir, Class: ClassNamespace, Label: "cache/go/build",
							Path: filepath.Join(dir, "build"), MaxSize: goBuildMaxSize,
							Reason: "go build cache over size cap (rebuildable)"},
						Action{Kind: KindEvictDir, Class: ClassNamespace, Label: "cache/go/downloads",
							Path: filepath.Join(dir, "downloads"), MaxAge: backstopMaxAge,
							Reason: "module cache backstop (re-downloadable)"})
				case "lint":
					age, size := defaultLintAge, int64(defaultLintSize)
					if cfg != nil {
						if d, err := config.ParseDuration(cfg.Lint.Cache.MaxAge); err == nil && cfg.Lint.Cache.MaxAge != "" {
							age = d
						}
						if s, err := config.ParseSize(cfg.Lint.Cache.MaxSize); err == nil && cfg.Lint.Cache.MaxSize != "" {
							size = s
						}
					}
					actions = append(actions, Action{Kind: KindEvictDir, Class: ClassNamespace,
						Label: "cache/lint", Path: dir, MaxAge: age, MaxSize: size,
						Reason: "lint result sets beyond lint.cache retention"})
				case "buildkit":
					age, size := defaultBuildkitAge, defaultBuildkitSize
					if cfg != nil {
						ret := cfg.BuildCache.Local.Retention
						if d, err := config.ParseDuration(ret.MaxAge); err == nil && ret.MaxAge != "" {
							age = d
						}
						if s, err := config.ParseSize(ret.MaxSize); err == nil && ret.MaxSize != "" {
							size = s
						}
					}
					actions = append(actions, Action{Kind: KindEvictDir, Class: ClassNamespace,
						Label: "cache/buildkit", Path: dir, MaxAge: age, MaxSize: size,
						Reason: "local buildkit cache beyond build_cache.local.retention"})
				default:
					// The backstop: every cache subsystem — including ones that do not
					// exist yet — is bounded on arrival. SF created every byte here.
					actions = append(actions, Action{Kind: KindEvictDir, Class: ClassNamespace,
						Label: "cache/" + sub, Path: dir, MaxAge: backstopMaxAge,
						Reason: "cache-root backstop (unused > 30d)"})
				}
			}
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
