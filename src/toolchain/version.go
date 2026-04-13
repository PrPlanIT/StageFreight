package toolchain

import "github.com/PrPlanIT/StageFreight/src/config"

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
