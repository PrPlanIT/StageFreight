package engines

// ContainerMeta is the engine-specific metadata for a containerized build step:
// run Command inside Image with the repo mounted, then collect Artifact (a glob
// of the produced file(s)). Attached to UniversalStep.Meta for the node engine.
type ContainerMeta struct {
	Image    string            `json:"image"`
	Command  string            `json:"command"`
	WorkDir  string            `json:"work_dir,omitempty"` // relative to repo root
	Env      map[string]string `json:"env,omitempty"`
	Artifact string            `json:"artifact"` // glob (relative to repo root) of produced file(s)
}

// StepMetaKind returns the kind identifier for containerized build steps.
func (m ContainerMeta) StepMetaKind() string { return "container" }
