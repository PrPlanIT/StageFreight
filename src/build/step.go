package build

import (
	"encoding/json"
	"time"
)

// Platform is a normalized os/arch pair.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// String returns "os/arch".
func (p Platform) String() string {
	return p.OS + "/" + p.Arch
}

// ParsePlatform splits "os/arch" into a Platform.
func ParsePlatform(s string) Platform {
	parts := splitPlatform(s)
	if len(parts) != 2 {
		return Platform{OS: s}
	}
	return Platform{OS: parts[0], Arch: parts[1]}
}

// ParsePlatforms parses a slice of "os/arch" strings.
func ParsePlatforms(ss []string) []Platform {
	out := make([]Platform, len(ss))
	for i, s := range ss {
		out[i] = ParsePlatform(s)
	}
	return out
}

func splitPlatform(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

// ArtifactRef is a declared artifact reference (expected input or output).
type ArtifactRef struct {
	Path string `json:"path"`
	Type string `json:"type"` // "binary", "image", "archive", "metadata"
}

// StepMeta is implemented by typed metadata structs attached to build steps.
// Every Meta type must be JSON-marshalable and return a stable kind string.
type StepMeta interface {
	StepMetaKind() string
}

// UniversalStep is a single unit of work in a build plan.
// Both image and binary engines produce steps in this shape.
type UniversalStep struct {
	BuildID  string        `json:"build_id"`  // which build config this belongs to
	StepID   string        `json:"step_id"`   // unique: {build_id}-{variant}-{os}-{arch}
	Engine   string        `json:"engine"`    // "image" | "binary"
	Platform Platform      `json:"platform"`
	Inputs   []ArtifactRef `json:"inputs,omitempty"`
	Outputs  []ArtifactRef `json:"outputs,omitempty"`
	Meta     StepMeta      `json:"-"` // engine-specific typed metadata
}

// MarshalJSON encodes UniversalStep with Meta as a typed JSON object.
func (s UniversalStep) MarshalJSON() ([]byte, error) {
	type Alias UniversalStep
	metaJSON, err := json.Marshal(s.Meta)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Alias
		MetaKind string          `json:"meta_kind"`
		Meta     json.RawMessage `json:"meta"`
	}{
		Alias:    Alias(s),
		MetaKind: s.Meta.StepMetaKind(),
		Meta:     metaJSON,
	})
}

// ProducedArtifact is an observed output from a step execution.
type ProducedArtifact struct {
	Path   string `json:"path"`
	Type   string `json:"type"` // "binary", "image", "archive", "metadata"
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// StepMetrics captures timing and cache information for a step execution.
type StepMetrics struct {
	Duration time.Duration `json:"duration"`
	Cached   bool          `json:"cached"`
}

// UniversalStepResult is what an engine returns after executing a single step.
type UniversalStepResult struct {
	Artifacts []ProducedArtifact `json:"artifacts"`
	Metadata  map[string]string  `json:"metadata,omitempty"` // e.g., {"toolchain": "go1.24.1"}
	Metrics   StepMetrics        `json:"metrics"`
}

// Capabilities declares what a build engine can do.
// Core queries behavior, never engine names.
type Capabilities struct {
	SupportsCrossCompile bool `json:"supports_cross_compile"`
	SupportsCrucible     bool `json:"supports_crucible"`
	ProducesArchives     bool `json:"produces_archives"`
	ProducesOCI          bool `json:"produces_oci"`
}

// StepIDForPlatform builds a deterministic step ID.
// Format: {buildID}-{variant}-{os}-{arch} (variant slot reserved, empty for now).
func StepIDForPlatform(buildID string, p Platform) string {
	return buildID + "--" + p.OS + "-" + p.Arch
}
