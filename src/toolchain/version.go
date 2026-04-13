package toolchain

import (
	"sort"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// ResolveVersion determines the version to use for a tool.
//
// Precedence (strict):
//  1. requestedVersion — explicit caller override (non-empty = use it)
//  2. desired[tool].Version — config pin (authoritative, not a hint)
//  3. ToolDef.DefaultVer — hardcoded fallback
//
// Returns the version string and whether it came from a config pin.
// The isPinned flag is critical: pinned versions that fail to resolve
// must hard-fail with no fallback to default.
func ResolveVersion(tool, requestedVersion string, desired map[string]config.ToolPinConfig) (version string, isPinned bool) {
	if requestedVersion != "" {
		return requestedVersion, false
	}
	if desired != nil {
		if pin, ok := desired[tool]; ok && pin.Version != "" {
			return pin.Version, true
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
func SeedDesired() map[string]config.ToolPinConfig {
	desired := make(map[string]config.ToolPinConfig)
	for _, def := range AllTools() {
		if def.Name == "" || def.Name == "go" {
			continue // Go version is authoritative from go.mod, not desired config
		}
		desired[def.Name] = config.ToolPinConfig{Version: def.DefaultVer}
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
