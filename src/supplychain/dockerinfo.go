package supplychain

// DockerFreshnessInfo holds everything extracted from a Dockerfile
// relevant to freshness checking.
type DockerFreshnessInfo struct {
	Stages      []StageInfo
	EnvVars     map[string]EnvVar
	PinnedTools []PinnedTool
	ApkPackages []PackageRef
	AptPackages []PackageRef
	PipPackages []PackageRef
}

// StageInfo describes a single Dockerfile FROM stage.
type StageInfo struct {
	Image string // full image reference (e.g. "golang:1.25-alpine")
	Name  string // AS alias
	Line  int
}

// EnvVar describes a single Dockerfile ENV declaration.
type EnvVar struct {
	Name  string
	Value string
	Line  int
}

// PinnedTool describes a pinned tool version resolved from an ENV
// *_VERSION variable cross-referenced with a GitHub release URL.
type PinnedTool struct {
	EnvName string // e.g. "BUILDX_VERSION"
	Version string // e.g. "v0.31.1"
	Owner   string // GitHub owner
	Repo    string // GitHub repo
	Line    int    // line of the ENV declaration
}

// PackageRef describes a single package reference extracted from a RUN
// install line (apk/apt/pip).
type PackageRef struct {
	Name    string
	Version string // empty if unpinned
	Line    int
}
