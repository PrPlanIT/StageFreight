package config

import "sort"

// This file exposes the recognized values of enum-like config fields so tooling —
// notably the docs generator — can list allowed values straight from the authoritative
// source instead of hand-maintaining (and drifting from) a parallel list.

// sortedEnum returns the non-empty keys of a validation set, sorted. The empty-string
// sentinel that several sets use for "unset / default" is dropped — it isn't a value a
// user would type.
func sortedEnum(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidTargetKinds returns the recognized targets[].kind values.
func ValidTargetKinds() []string { return sortedEnum(validTargetKinds) }

// ValidArchiveFormats returns the recognized binary-archive format values.
func ValidArchiveFormats() []string { return sortedEnum(validArchiveFormats) }

// ValidEvents returns the recognized targets[].when.events values.
func ValidEvents() []string { return sortedEnum(validEvents) }

// ValidManifestModes returns the recognized manifest.mode values.
func ValidManifestModes() []string { return sortedEnum(validManifestModes) }

// ValidNarratorItemKinds returns the recognized narrate item kinds.
func ValidNarratorItemKinds() []string { return sortedEnum(validNarratorItemKinds) }

// ValidPlacementModes returns the recognized placement.mode values.
func ValidPlacementModes() []string { return sortedEnum(validPlacementModes) }

// ValidTrustClasses returns the recognized signing_profiles[].requires values.
func ValidTrustClasses() []string { return sortedEnum(validTrustClasses) }

// ValidLintLevels returns the recognized lint.level values.
func ValidLintLevels() []string { return []string{string(LevelChanged), string(LevelFull)} }

// validBuildKinds enumerates recognized builds[].kind values.
var validBuildKinds = map[string]bool{"binary": true, "command": true, "docker": true}

// validBuilders enumerates recognized builds[].builder values (kind: binary).
var validBuilders = map[string]bool{
	"go": true, "rust": true, "node": true, "elixir": true, "dotnet": true,
	"c": true, "python": true, "jvm": true, "android": true,
}

// validOutputTypes enumerates recognized command-output (builds[].outputs[].type) values.
var validOutputTypes = map[string]bool{"tree": true, "file": true, "binary": true}

// ValidBuildKinds returns the recognized builds[].kind values.
func ValidBuildKinds() []string { return sortedEnum(validBuildKinds) }

// ValidBuilders returns the recognized builds[].builder values.
func ValidBuilders() []string { return sortedEnum(validBuilders) }

// ValidOutputTypes returns the recognized builds[].outputs[].type values.
func ValidOutputTypes() []string { return sortedEnum(validOutputTypes) }
