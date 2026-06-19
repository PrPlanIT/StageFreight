package engines

// RustMeta is the engine-specific metadata for a Rust (cargo) binary build step —
// the Rust analog of BinaryMeta. Attached to UniversalStep.Meta for binary-rust steps.
type RustMeta struct {
	ManifestDir string            `json:"manifest_dir"` // dir containing Cargo.toml
	BinName     string            `json:"bin_name"`     // crate [[bin]]/package name → cargo --bin
	OutputPath  string            `json:"output_path"`  // canonical DistDir output path
	Release     bool              `json:"release"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// StepMetaKind returns the kind identifier for Rust build steps.
func (m RustMeta) StepMetaKind() string { return "rust" }
