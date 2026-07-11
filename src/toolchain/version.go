package toolchain

import (
	"sort"

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
	}
	if def, ok := LookupTool(tool); ok {
		return def.DefaultVer, false
	}
	return "", false
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
