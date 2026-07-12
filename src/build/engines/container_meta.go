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
	// ForwardEnv names host env vars (e.g. CI secrets) to pass into the build
	// container by value — used for signing secrets like an Android keystore.
	ForwardEnv []string `json:"forward_env,omitempty"`
	// CacheSubdir locates the persistent package-manager store under the SF cache root
	// (e.g. ["node","pnpm-store"]); CacheEnv is the env var pointing the tool at the
	// in-container mount of it. Empty disables caching (cold build).
	CacheSubdir []string `json:"cache_subdir,omitempty"`
	CacheEnv    string   `json:"cache_env,omitempty"`
}

// StepMetaKind returns the kind identifier for containerized build steps.
func (m ContainerMeta) StepMetaKind() string { return "container" }
