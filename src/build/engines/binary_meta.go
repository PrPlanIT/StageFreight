package engines

// BinaryMeta is the engine-specific metadata for a binary build step.
// Attached to UniversalStep.Meta for binary engine steps.
type BinaryMeta struct {
	Entry      string            `json:"entry"`
	BinaryName string            `json:"binary_name"`
	OutputPath string            `json:"output_path"`
	LDFlags    []string          `json:"ldflags,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Strip      bool              `json:"strip"`
	Compress   bool              `json:"compress"`
}

// StepMetaKind returns the kind identifier for binary build steps.
func (m BinaryMeta) StepMetaKind() string { return "binary/go" }
