package build

import (
	"encoding/json"
	"time"
)

// Target is the canonical semantic identity of a build target: OS, architecture,
// and (when it matters) the C library and ABI. This is the ONE target abstraction
// shared by every language engine — Go reads OS/Arch (and ignores Libc/ABI), Rust
// PROJECTS the same fields into a target triple. The triple is therefore a derivation
// of this identity, never stored here and never the primary key, so the abstraction
// stays language-neutral rather than quietly becoming Rust-centric.
//
// Libc is "" for targets where it is irrelevant or default (every Go target today);
// it becomes load-bearing the moment musl enters ("linux/amd64" stops being a unique
// identity once both gnu and musl exist). The derived names (String, step ID, output
// path, archive name) append Libc only when set, so libc-less targets — i.e. all
// current Go builds — render byte-identically to before.
type Target struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Libc string `json:"libc,omitempty"` // "", "gnu", "musl"
	ABI  string `json:"abi,omitempty"`  // optional/future (e.g. "eabihf")
}

// String returns "os/arch" (or "os/arch/libc" when a libc is set).
func (t Target) String() string {
	s := t.OS + "/" + t.Arch
	if t.Libc != "" {
		s += "/" + t.Libc
	}
	return s
}

// Slug returns a filesystem/ID-safe "os-arch" (or "os-arch-libc") fragment — the
// canonical fragment for output dirs, step IDs, and archive names.
func (t Target) Slug() string {
	s := t.OS + "-" + t.Arch
	if t.Libc != "" {
		s += "-" + t.Libc
	}
	return s
}

// ParseTarget splits "os/arch" or "os/arch/libc" into a Target.
func ParseTarget(s string) Target {
	parts := splitN(s, '/')
	switch len(parts) {
	case 3:
		return Target{OS: parts[0], Arch: parts[1], Libc: parts[2]}
	case 2:
		return Target{OS: parts[0], Arch: parts[1]}
	default:
		return Target{OS: s}
	}
}

// ParseTargets parses a slice of "os/arch[/libc]" strings.
func ParseTargets(ss []string) []Target {
	out := make([]Target, len(ss))
	for i, s := range ss {
		out[i] = ParseTarget(s)
	}
	return out
}

func splitN(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
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
	BuildID string        `json:"build_id"` // which build config this belongs to
	StepID  string        `json:"step_id"`  // unique: {build_id}-{variant}-{os}-{arch}
	Engine  string        `json:"engine"`   // "image" | "binary-go" | "binary-rust" — the dispatch key
	Target  Target        `json:"target"`
	Inputs  []ArtifactRef `json:"inputs,omitempty"`
	Outputs []ArtifactRef `json:"outputs,omitempty"`
	Meta    StepMeta      `json:"-"` // engine-specific typed metadata
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

// StepIDForTarget builds a deterministic step ID.
// Format: {buildID}--{os}-{arch}[-{libc}] (variant slot reserved by the "--").
func StepIDForTarget(buildID string, t Target) string {
	return buildID + "--" + t.Slug()
}
