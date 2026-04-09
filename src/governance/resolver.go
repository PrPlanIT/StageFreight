// Package governance re-exports preset resolution from src/config.
// Preset resolution belongs to the config layer — governance is a consumer.
// All callers of governance.ResolvePresets / MergeEntry / MergeTrace continue
// to work unchanged via these type and var aliases.
package governance

import "github.com/PrPlanIT/StageFreight/src/config"

// Type aliases — identical types, no conversion needed at call sites.
type PresetLoader = config.PresetLoader
type MergeEntry = config.MergeEntry
type MergeTrace = config.MergeTrace

// Function aliases.
var ResolvePresets = config.ResolvePresets
var ValidatePreset = config.ValidatePreset
var DeepMerge = config.DeepMerge
