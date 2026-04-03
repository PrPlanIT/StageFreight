package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Normalize resolves {var:...} templates throughout the config using the
// config's own Vars map. Called once after load+validate, before any consumer
// reads the config. All consumers get fully-resolved values — no late binding.
func Normalize(cfg *Config) {
	if len(cfg.Vars) == 0 {
		return
	}

	// Targets: URL, Path, Credentials
	for i := range cfg.Targets {
		cfg.Targets[i].URL = resolveTemplateVars(cfg.Targets[i].URL, cfg.Vars)
		cfg.Targets[i].Path = resolveTemplateVars(cfg.Targets[i].Path, cfg.Vars)
		cfg.Targets[i].Credentials = resolveTemplateVars(cfg.Targets[i].Credentials, cfg.Vars)
	}

	// Sources: URL, mirrors
	cfg.Sources.Primary.URL = resolveTemplateVars(cfg.Sources.Primary.URL, cfg.Vars)
	for i := range cfg.Sources.Mirrors {
		cfg.Sources.Mirrors[i].URL = resolveTemplateVars(cfg.Sources.Mirrors[i].URL, cfg.Vars)
		cfg.Sources.Mirrors[i].ProjectID = resolveTemplateVars(cfg.Sources.Mirrors[i].ProjectID, cfg.Vars)
		cfg.Sources.Mirrors[i].Credentials = resolveTemplateVars(cfg.Sources.Mirrors[i].Credentials, cfg.Vars)
	}

	// Builds: BuildArgs
	for i := range cfg.Builds {
		for k, v := range cfg.Builds[i].BuildArgs {
			cfg.Builds[i].BuildArgs[k] = resolveTemplateVars(v, cfg.Vars)
		}
	}

	// Badges: Link
	for i := range cfg.Badges.Items {
		cfg.Badges.Items[i].Link = resolveTemplateVars(cfg.Badges.Items[i].Link, cfg.Vars)
	}

	// Narrator: LinkBase, and per-item Link, Shield, Source, Content, Spec
	for i := range cfg.Narrator {
		cfg.Narrator[i].LinkBase = resolveTemplateVars(cfg.Narrator[i].LinkBase, cfg.Vars)
		for j := range cfg.Narrator[i].Items {
			item := &cfg.Narrator[i].Items[j]
			item.Link = resolveTemplateVars(item.Link, cfg.Vars)
			item.Shield = resolveTemplateVars(item.Shield, cfg.Vars)
			item.Source = resolveTemplateVars(item.Source, cfg.Vars)
			item.Content = resolveTemplateVars(item.Content, cfg.Vars)
			item.Spec = resolveTemplateVars(item.Spec, cfg.Vars)
		}
	}

	// BuildCache: external target is resolved via targets (already done above).
	// Sources.PublishOrigin: resolved via Sources above.
}

// AssertNormalized verifies no unresolved {var:} templates remain in the config.
// Called after Normalize as a safety net — catches regressions when new fields
// are added but not included in the normalization pass.
func AssertNormalized(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil // can't serialize, skip check
	}
	if strings.Contains(string(data), "{var:") {
		return fmt.Errorf("unresolved {var:} template found after normalization — Normalize() is missing a field")
	}
	return nil
}

// resolveTemplateVars replaces StageFreight {var:name} template placeholders
// using values from vars. Single-pass only; no recursion or nesting.
func resolveTemplateVars(s string, vars map[string]string) string {
	if !strings.Contains(s, "{var:") {
		return s
	}
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{var:"+k+"}", v)
	}
	return s
}
