package config

// BuildConfig defines a named build artifact. Each build has a unique ID
// (referenced by targets) and a kind that determines which fields are valid.
//
// This is a discriminated union keyed by Kind — only fields relevant to the
// kind should be set. Validated at load time by v2 validation.
type BuildConfig struct {
	// ID is the unique identifier for this build, referenced by targets.
	ID string `yaml:"id"`

	// Kind is the build type. Determines which fields are valid.
	// Supported: "docker", "binary".
	Kind string `yaml:"kind"`

	// SelectTags enables CLI filtering via --select.
	SelectTags []string `yaml:"select_tags,omitempty"`

	// BuildMode controls the build execution strategy.
	// Supported: "" (standard), "crucible" (self-proving rebuild).
	BuildMode string `yaml:"build_mode,omitempty"`

	// DependsOn references another build ID that must complete before this one.
	// Enables build ordering: binary builds before docker builds that consume them.
	DependsOn string `yaml:"depends_on,omitempty"`

	// ── kind: docker ──────────────────────────────────────────────────────

	// Dockerfile is the path to the Dockerfile. Default: auto-detect.
	Dockerfile string `yaml:"dockerfile,omitempty"`

	// Context is the Docker build context path. Default: "." (repo root).
	Context string `yaml:"context,omitempty"`

	// Target is the --target stage name for multi-stage builds.
	Target string `yaml:"target,omitempty"`

	// Platforms lists the target platforms. Default: [linux/{current_arch}].
	Platforms []string `yaml:"platforms,omitempty"`

	// BuildArgs are key-value pairs passed as --build-arg. Supports templates.
	BuildArgs map[string]string `yaml:"build_args,omitempty"`

	// Cache holds build cache settings.
	Cache CacheConfig `yaml:"cache,omitempty"`

	// ── kind: binary ──────────────────────────────────────────────────────

	// Language is the compilation language. Supported: "go". Future: "rust", "zig".
	Language string `yaml:"language,omitempty"`

	// Entry is the main package path (Go) or source file.
	// e.g., "cmd/planedc/main.go" or "cmd/planedc"
	Entry string `yaml:"entry,omitempty"`

	// BinaryName is the output binary name. Default: basename of entry.
	// Windows platforms auto-append ".exe".
	BinaryName string `yaml:"binary_name,omitempty"`

	// Output is the output path template. Supports: {id}, {os}, {arch}, {version}, {binary_name}.
	// Default: "dist/{os}-{arch}/{binary_name}"
	Output string `yaml:"output,omitempty"`

	// LDFlags are Go linker flags with template substitution.
	// e.g., ["-s -w", "-X main.Version={version}"]
	LDFlags []string `yaml:"ldflags,omitempty"`

	// Env are build environment variables. e.g., {"CGO_ENABLED": "0"}
	Env map[string]string `yaml:"env,omitempty"`

	// Strip removes debug symbols from the binary. Default: true for kind: binary.
	Strip *bool `yaml:"strip,omitempty"`

	// Compress enables UPX compression. Default: false.
	Compress bool `yaml:"compress,omitempty"`

	// Crucible holds crucible-specific configuration for binary builds.
	Crucible *CrucibleConfig `yaml:"crucible,omitempty"`
}

// CrucibleConfig holds crucible-specific build configuration.
type CrucibleConfig struct {
	// ToolchainImage is the pinned container image for pass-2 verification.
	// e.g., "docker.io/library/golang:1.24-alpine"
	ToolchainImage string `yaml:"toolchain_image,omitempty"`
}

// StripEnabled returns whether strip is enabled, defaulting to true for binary builds.
func (b BuildConfig) StripEnabled() bool {
	if b.Strip != nil {
		return *b.Strip
	}
	return b.Kind == "binary"
}
