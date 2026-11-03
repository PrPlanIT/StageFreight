package engines

import "github.com/PrPlanIT/StageFreight/src/config"

// ContainerMeta is the engine-specific metadata for a containerized build step:
// run Command inside Image with the repo staged in, then collect Artifact (a glob
// of the produced file(s)). Attached to UniversalStep.Meta for the container engines.
type ContainerMeta struct {
	Image    string            `json:"image"`
	Command  string            `json:"command"`
	WorkDir  string            `json:"work_dir,omitempty"` // relative to repo root
	Env      map[string]string `json:"env,omitempty"`
	Artifact string            `json:"artifact"` // glob (relative to repo root) of produced file(s)
	// ForwardEnv names host env vars (e.g. CI secrets) to pass into the build
	// by value — used for signing secrets like an Android keystore.
	ForwardEnv []string `json:"forward_env,omitempty"`
	// CacheSubdir names the persistent package-manager store (e.g.
	// ["node","pnpm-store"]); CacheEnv is the env var pointing the tool at it.
	// Empty disables caching (cold build).
	CacheSubdir []string `json:"cache_subdir,omitempty"`
	CacheEnv    string   `json:"cache_env,omitempty"`
	// InJob runs the command in the job itself (Image is the job's image); otherwise BuildKit builds on BuildxBuilder.
	InJob         bool                 `json:"in_job,omitempty"`
	BuildxBuilder config.BuilderConfig `json:"-"`
}

// StepMetaKind returns the kind identifier for containerized build steps.
func (m ContainerMeta) StepMetaKind() string { return "container" }
