package toolchain

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/config"
	depversion "github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// ResolveVersion determines the concrete version to provision for a tool.
//
// Precedence (strict):
//  1. requestedVersion — explicit caller override (non-empty = use it)
//  2. desired[tool].Constraint — config intent: EXACT is itself the version; a WILDCARD
//     (1.26.x) resolves to its locked version from .stagefreight/toolchains.lock (read
//     from rootDir). A wildcard with no lock yet is not provisionable from a range, so
//     it falls through to the default until an update pass writes the lock.
//  3. ToolDef.DefaultVer — hardcoded fallback
//
// Returns the version string and whether it came from config intent (isPinned). The
// isPinned flag is critical: pinned versions that fail to resolve must hard-fail with no
// fallback to default. rootDir locates the lock; pass "" when unknown (wildcards then
// fall back to default, exact pins are unaffected).
func ResolveVersion(rootDir, tool, requestedVersion string, desired map[string]config.ToolConstraint) (version string, isPinned bool) {
	if requestedVersion != "" {
		return requestedVersion, false
	}
	unlocked := ""
	if pin, ok := desired[tool]; ok && pin.Constraint != "" {
		if !depversion.IsWildcardConstraint(pin.Constraint) {
			return pin.Constraint, true // exact — the constraint IS the version
		}
		if rootDir != "" {
			if lock, err := ReadLock(rootDir); err == nil {
				if v := lock.Resolved(tool); v != "" {
					return v, true
				}
			}
		}
		unlocked = pin.Constraint
	}
	if def, ok := LookupTool(tool); ok {
		if unlocked != "" {
			warnUnlocked(tool, unlocked, def.DefaultVer)
		}
		return def.DefaultVer, false
	}
	return "", false
}

// HostLibc is the running host's libc: "musl" or "gnu".
func HostLibc() string {
	if isMuslLibc() {
		return "musl"
	}
	return "gnu"
}

// Variant is the libc build to use for def: requested when set (it must be one def
// publishes), else the host's; "" for a tool without libc builds.
func Variant(def ToolDef, requested string) (string, error) {
	if len(def.Libcs) == 0 {
		if requested != "" {
			return "", fmt.Errorf("toolchain %s publishes no libc variants; remove variant: %s", def.Name, requested)
		}
		return "", nil
	}
	want := requested
	if want == "" {
		want = HostLibc()
	}
	for _, l := range def.Libcs {
		if l == want {
			return want, nil
		}
	}
	if requested != "" {
		return "", fmt.Errorf("toolchain %s publishes no %s build (has %v)", def.Name, requested, def.Libcs)
	}
	return def.Libcs[0], nil
}

// PlatformKey is the digest and cache key of a build on this host: os/arch, suffixed
// -musl for a musl build.
func PlatformKey(variant string) string {
	key := runtime.GOOS + "/" + runtime.GOARCH
	if variant == "musl" {
		key += "-musl"
	}
	return key
}

// HostPlatformKey is PlatformKey for the build this host uses of tool.
func HostPlatformKey(tool string) string {
	def, ok := LookupTool(tool)
	if !ok {
		return PlatformKey("")
	}
	v, err := Variant(def, "")
	if err != nil {
		return PlatformKey("")
	}
	return PlatformKey(v)
}

// Selection is what to provision for a tool: the version, its libc build, and the
// artifact digest the download must match ("" verifies by upstream checksum or first use).
type Selection struct {
	Version string
	Variant string // libc build; "" for a tool without libc builds
	SHA256  string
	Pinned  bool // the version came from config intent; a failed resolve is fatal
	Exact   bool // config declared this exact version (not a line tracked through the lock)
}

// Select resolves tool's version (ResolveVersion), its libc build (variant: or the
// host's), and its expected digest for that build: the config's sha256 when declared for
// that exact version (a missing platform is an error), else the lock's.
func Select(rootDir, tool string, desired map[string]config.ToolConstraint) (Selection, error) {
	if rootDir != "" {
		if _, err := ReadLock(rootDir); err != nil {
			return Selection{}, err // an unreadable lock must not silently fall back to the default
		}
	}
	ver, pinned := ResolveVersion(rootDir, tool, "", desired)
	c := desired[tool]
	sel := Selection{Version: ver, Pinned: pinned, Exact: pinned && c.Constraint == ver}
	if def, ok := LookupTool(tool); ok {
		v, err := Variant(def, c.Variant)
		if err != nil {
			return sel, fmt.Errorf("toolchains.want.%s.variant: %w", tool, err)
		}
		sel.Variant = v
	}
	key := PlatformKey(sel.Variant)
	if len(c.SHA256) > 0 && c.Constraint == ver {
		d, ok := c.SHA256[key]
		if !ok {
			return sel, fmt.Errorf("toolchains.want.%s.sha256 declares no digest for %s", tool, key)
		}
		sel.SHA256 = d
		return sel, nil
	}
	if rootDir != "" {
		if lock, err := ReadLock(rootDir); err == nil {
			sel.SHA256 = lock.Digest(tool, ver, key)
		}
	}
	return sel, nil
}

var warnedUnlocked sync.Map

// warnUnlocked discloses, once per tool per process, that a wildcard constraint ran the
// built-in default instead of a resolved version.
func warnUnlocked(tool, constraint, used string) {
	if _, seen := warnedUnlocked.LoadOrStore(tool, true); seen {
		return
	}
	fmt.Fprintf(os.Stderr, "  ⚠ toolchain %s %q has no entry in .stagefreight/toolchains.lock — running the built-in default %s, not the newest in range\n", tool, constraint, used)
}

// SeedDesired returns a complete map of all managed tools with their current
// default versions. Used to populate toolchains.desired for new repos so that
// all managed tools are explicitly materialized in config from the start.
// Go is excluded — its version comes from go.mod, not the toolchain registry.
func SeedDesired() map[string]config.ToolConstraint {
	desired := make(map[string]config.ToolConstraint)
	for _, def := range AllTools() {
		if def.Name == "" || def.Name == "go" {
			continue // Go version is authoritative from go.mod, not desired config
		}
		desired[def.Name] = config.ToolConstraint{Constraint: def.DefaultVer}
	}
	return desired
}

// ManagedToolNames returns a sorted list of all managed tool names.
func ManagedToolNames() []string {
	var names []string
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
