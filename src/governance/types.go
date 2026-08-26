// Package governance implements the StageFreight governance engine:
// preset resolution, two-file config merge, governance reconciliation,
// capability detection, and execution gating.
package governance

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// GovernanceSource declares where governance inputs come from.
// Declared in .stagefreight.yml under governance.source.
type GovernanceSource struct {
	RepoURL       string `yaml:"repo_url"`       // policy repo URL
	Ref           string `yaml:"ref"`            // pinned tag or commit SHA (required)
	Path          string `yaml:"path"`           // path to governance config within repo
	AllowFloating bool   `yaml:"allow_floating"` // if true, branch refs allowed (dev/unsafe)
	LocalPath     string `yaml:"-"`              // if set, use local checkout instead of cloning
}

// GovernanceConfig is the parsed governance config from the policy repo.
type GovernanceConfig struct {
	Profiles ProfileList `yaml:"profiles"`
}

// ProfileList is the profiles: block — an id→profile map (the map key becomes
// Profile.ID). Governance decodes its own id-map (the config package's decodeIDMap is
// unexported); document order is preserved.
type ProfileList []Profile

func (pl *ProfileList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("profiles: must be an id → profile map")
	}
	var out []Profile
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		var p Profile
		if err := n.Content[i+1].Decode(&p); err != nil {
			return fmt.Errorf("profiles[%s]: %w", key, err)
		}
		p.ID = key
		out = append(out, p)
	}
	*pl = out
	return nil
}

// Profile assigns shared policy + branding to a group of repos (its catalog). Config is
// normal StageFreight config; Repos is the location-anchored catalog. Assets are
// declared inside Config as assets: entries — no separate skeleton construct.
type Profile struct {
	ID          string         `yaml:"-"`                     // from the profiles: map key
	Repos       ProfileCatalog `yaml:"repos"`                 // the location-anchored catalog
	Config      map[string]any `yaml:"config"`                // the profile's shared StageFreight config
	Credentials string         `yaml:"credentials,omitempty"` // env var prefix for the write token
}

// ProfileCatalog is a profile's repos: block — an id→entry map, each entry anchored on
// a repo location.
type ProfileCatalog []CatalogEntry

func (pc *ProfileCatalog) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("repos: must be an id → entry map")
	}
	var out []CatalogEntry
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		e := CatalogEntry{ID: key}
		if err := e.decode(n.Content[i+1]); err != nil {
			return fmt.Errorf("repos[%s]: %w", key, err)
		}
		out = append(out, e)
	}
	*pc = out
	return nil
}

// CatalogEntry is one governed repo: a location anchor plus optional governed branding
// and an optional per-repo config override. A bare-string entry is location-only (CI
// governed, identity self-authored); a map entry (at: + fields) governs identity too.
type CatalogEntry struct {
	ID       string         // the repos: map key (a label)
	Forge    string         // forge id from a "<forge>:" location prefix, or "" (inherit)
	At       string         // the repo project path ("<group>/<repo>")
	Metadata map[string]any // governed branding (nil for a bare-string / location-only entry)
	Config   map[string]any // optional per-repo config override
}

func (e *CatalogEntry) decode(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		e.Forge, e.At = splitLocation(n.Value)
		return nil
	case yaml.MappingNode:
		raw := map[string]any{}
		if err := n.Decode(&raw); err != nil {
			return err
		}
		at, _ := raw["at"].(string)
		if at == "" {
			return fmt.Errorf("a map entry must have `at` (the repo location)")
		}
		e.Forge, e.At = splitLocation(at)
		delete(raw, "at")
		if cfg, ok := raw["config"].(map[string]any); ok {
			e.Config = cfg
		}
		delete(raw, "config")
		if len(raw) > 0 {
			e.Metadata = raw // everything else is governed branding
		}
		return nil
	default:
		return fmt.Errorf("entry must be a location string or a map with `at`")
	}
}

// splitLocation splits "<forge>:<path>" into (forge, path); a bare location (no
// forge-prefix colon before the first slash) returns ("", loc).
func splitLocation(loc string) (forge, path string) {
	if i := strings.IndexByte(loc, ':'); i > 0 && !strings.Contains(loc[:i], "/") {
		return loc[:i], loc[i+1:]
	}
	return "", loc
}

// PresetRef is a reference to an external preset fragment.
// Appears as preset: "path" within any config section.
type PresetRef struct {
	Path string
}

// ResolvedPreset is a loaded and validated preset fragment.
type ResolvedPreset struct {
	Path        string         // source path within policy repo
	TopLevelKey string         // the single top-level key this preset declares
	Content     map[string]any // parsed YAML content under that key
}

// DetectionReport is the output of capability discovery.
type DetectionReport struct {
	Capabilities []CapabilityResult
}

// CapabilityResult records whether a specific capability was detected.
type CapabilityResult struct {
	Domain     string // e.g., "build.docker", "build.binary", "package.helm"
	Detected   bool
	Confidence string   // "high", "medium", "low"
	Evidence   []string // filesystem signals that supported detection
}

// ExecutionPlan is the gated output — what will actually run.
// Produced by GateExecution. Does NOT modify config.
type ExecutionPlan struct {
	Enabled []EnabledFeature
	Skipped []SkippedFeature
}

// EnabledFeature is a feature that passed both config and capability gates.
type EnabledFeature struct {
	Domain string
	Reason string // "config enabled + capability detected"
}

// SkippedFeature is a feature that was gated out.
type SkippedFeature struct {
	Domain string
	Reason string // "capability not detected" or "config disabled"
}

// DistributionPlan describes what files to write to a target repo.
type DistributionPlan struct {
	Repo        string // "org/repo"
	Credentials string // env var prefix for forge auth (from cluster targets)
	Files       []DistributedFile
}

// DistributedFile is a single file to write/update in a target repo.
type DistributedFile struct {
	Path    string // e.g., ".stagefreight/stagefreight-managed.yml"
	Content []byte
	Action  string // "create", "replace", "unchanged", "delete"
	Drifted bool   // true if existing file differs from governance intent
}

// CommitResult records what happened for each repo during distribution.
type CommitResult struct {
	Repo    string
	Status  string // "committed", "unchanged", "dry-run", "skipped-identical", "error"
	SHA     string // commit SHA if committed
	Message string
	Drifted bool // true if managed file was drifted before replacement
	Error   error
}

// CanonicalKeyOrder defines the fixed top-level key order for rendered YAML.
// Prevents noisy diffs and unstable commits.
var CanonicalKeyOrder = []string{
	"version",
	"vars",
	"sources",
	"policies",
	"builds",
	"targets",
	"badges",
	"scribe",
	"lint",
	"security",
	"dependency",
	"docs",
	"commit",
	"release",
	"lifecycle",
	"gitops",
	"docker",
	"glossary",
	"presentation",
	"tag",
	"manifest",
}
