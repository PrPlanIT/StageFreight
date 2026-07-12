package docsgen

import (
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// enumSources binds a config field's full doc-path to the authoritative source of its
// allowed values, so the reference lists real, in-sync values rather than a hand-curated
// list that drifts. The source wins over any curated FieldOverride.AllowedValues.
var enumSources = map[string]func() []string{
	"targets.kind":              config.ValidTargetKinds,
	"targets.format":            config.ValidArchiveFormats,
	"targets.when.events":       config.ValidEvents,
	"signing_profiles.requires": config.ValidTrustClasses,
	"lint.level":                config.ValidLintLevels,
	"manifest.mode":             config.ValidManifestModes,
	"builds.kind":               config.ValidBuildKinds,
	"builds.builder":            config.ValidBuilders,
	"builds.outputs.type":       config.ValidOutputTypes,
	"registries.provider":       registry.KnownProviders,
	"forges.provider":           forge.KnownProviders,
}

// enumValuesFor returns the sourced allowed values for a config field doc-path, or nil.
func enumValuesFor(docPath string) []string {
	if fn, ok := enumSources[docPath]; ok {
		return fn()
	}
	return nil
}
