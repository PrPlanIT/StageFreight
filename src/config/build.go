package config

import (
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CommandSpec is a build command that accepts either a scalar shell string
// (`command: "go build"`) or a sequence / argv list (`command: [go, build]`),
// normalized to a single string for `sh -c`. The sequence form is shell-quoted so
// args with spaces survive. Used by binary builders (a subcommand) and kind: command
// (the full command).
type CommandSpec string

// UnmarshalYAML accepts a scalar or a sequence of strings.
func (c *CommandSpec) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.SequenceNode {
		var parts []string
		if err := value.Decode(&parts); err != nil {
			return err
		}
		*c = CommandSpec(shellJoin(parts))
		return nil
	}
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	*c = CommandSpec(s)
	return nil
}

// shellJoin joins argv parts into a POSIX sh command, single-quoting any part that
// contains characters the shell would interpret, so `[echo, a b]` → `echo 'a b'`.
func shellJoin(parts []string) string {
	quoted := make([]string, len(parts))
	for i, p := range parts {
		if p != "" && !strings.ContainsAny(p, " \t\n'\"\\$`&|;<>(){}[]*?~#!") {
			quoted[i] = p
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// OutputSpec declares one captured build output: what the command produced (Source,
// repo-relative) and its artifact class (Type ∈ tree | file | binary). Worktree opts the
// output into the git working tree during Narrate (so it can be committed or read by
// patches); absent, the output is a pure artifact (transported/published, never landed).
type OutputSpec struct {
	Type     string        `yaml:"type"`
	Source   string        `yaml:"source"`
	Worktree *WorktreeSpec `yaml:"worktree,omitempty"`
}

// WorktreeSpec is an output's optional landing into the git working tree during Narrate.
// It accepts a bool or a path: `worktree: true` lands the output at its Source path;
// `worktree: <path>` lands it there instead (a clean rename); `worktree: false` / absent
// leaves it as a pure artifact. The runner materializes every landed output at the start
// of Narrate — before badges/patches/commit — so downstream config deals only in paths.
type WorktreeSpec struct {
	Set  bool   // whether worktree was present and truthy
	Path string // explicit landing path; empty means "land at the output's Source"
}

// UnmarshalYAML accepts either a bool (`true`/`false`) or a path string.
func (w *WorktreeSpec) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag == "!!bool" {
		var b bool
		if err := value.Decode(&b); err != nil {
			return err
		}
		w.Set = b
		return nil
	}
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	w.Path = s
	w.Set = strings.TrimSpace(s) != ""
	return nil
}

// MarshalYAML renders back to the scalar form (`true`/`false` or the path) so
// `config render` stays clean.
func (w WorktreeSpec) MarshalYAML() (interface{}, error) {
	if w.Path != "" {
		return w.Path, nil
	}
	return w.Set, nil
}

// LandsInWorktree reports whether this output is materialized into the working tree.
func (o OutputSpec) LandsInWorktree() bool {
	return o.Worktree != nil && o.Worktree.Set
}

// WorktreePath is where the output lands in the repo: its explicit worktree path, or
// its Source path when worktree is just `true`.
func (o OutputSpec) WorktreePath() string {
	if o.Worktree != nil && o.Worktree.Path != "" {
		return o.Worktree.Path
	}
	return o.Source
}

// BuildConfig defines a named build artifact. Each build has a unique ID
// (referenced by targets) and a kind that determines which fields are valid.
//
// This is a discriminated union keyed by Kind — only fields relevant to the
// kind should be set. Validated at load time by v2 validation.
type BuildConfig struct {
	// ID is the unique identifier for this build, referenced by targets.
	ID string `yaml:"id"`

	// Kind is the build type. Determines which fields are valid.
	// Supported: "docker", "binary", "command".
	//   - docker:  build an OCI image
	//   - binary:  build an executable via a language builder (opinionated inference)
	//   - command: run an arbitrary command in an image, capture typed outputs (the
	//              un-opinionated escape hatch; no inference). Prefer a `builder:` when
	//              one fits; reach for command only when none does.
	Kind string `yaml:"kind"`

	// SelectTags enables CLI filtering via --select.
	SelectTags []string `yaml:"select_tags,omitempty"`

	// Required means build failure is a hard pipeline fail. Default: true.
	Required *bool `yaml:"required,omitempty"`

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

	// Stage recycles a binary build's output into this docker build's context before
	// buildx, so a copy-pre-built Dockerfile (COPY <name>) resolves — the compiled
	// binary is reused, not recompiled inside the image.
	Stage *StageConfig `yaml:"stage,omitempty"`

	// ── kind: binary ──────────────────────────────────────────────────────
	// Generic build schema: builder selects toolchain, args are raw vendor-native
	// arguments passed directly to the builder's command. No language-specific
	// config branches — one stable object model for all binary builders.

	// Builder is the toolchain that interprets the build.
	// Supported: "go", "rust", "node", "elixir", "dotnet", "c", "python", "jvm", "android"
	// (authoritative list: validBuilders in enums.go). Future: "zig". Empty defaults to "go".
	Builder string `yaml:"builder,omitempty"`

	// Command is the builder subcommand (binary: e.g. "build") or the full command
	// (kind: command). Accepts a scalar string or an argv sequence. Default: "build".
	Command CommandSpec `yaml:"command,omitempty"`

	// From is the source/input root or entry point.
	// e.g., "./src/cli" (Go package), "./src/main.rs" (Rust).
	From string `yaml:"from,omitempty"`

	// Output is the artifact name. Windows platforms auto-append ".exe".
	// Default: basename of From.
	Output string `yaml:"output,omitempty"`

	// Image is the container image a containerized build (builder: node, elixir) runs
	// inside (with the repo mounted). Command runs in it; Output is the produced
	// artifact (file or directory tree). Defaults per builder; override for the odd
	// case (e.g. electronuserland/builder:wine, or an elixir+node image for Phoenix).
	Image string `yaml:"image,omitempty"`

	// Args are ordered raw arguments passed directly to the selected builder.
	// For Go: raw args to "go build". For Rust: raw args to "cargo build".
	// Supports template variables: {version}, {sha}, {sha:N}, {date}.
	Args []string `yaml:"args,omitempty"`

	// Env are build environment variables. e.g., {"CGO_ENABLED": "0"}
	Env map[string]string `yaml:"env,omitempty"`

	// Compress enables UPX compression on the output binary. Default: false.
	Compress bool `yaml:"compress,omitempty"`

	// Crucible holds crucible-specific configuration for binary builds.
	Crucible *CrucibleConfig `yaml:"crucible,omitempty"`

	// ── kind: command ─────────────────────────────────────────────────────
	// Run Command in Image (default: ci.image), capture the declared Outputs. No
	// language inference — the un-opinionated escape hatch. Reuses Image/Command/Env.

	// Outputs declares what the command produced and each output's artifact class.
	Outputs []OutputSpec `yaml:"outputs,omitempty"`
}

// WorktreeOutput pairs a build with one of its outputs that lands in the working tree.
type WorktreeOutput struct {
	BuildID string
	Output  OutputSpec
}

// WorktreeOutputs returns every build output opted into the working tree
// (outputs[].worktree) across all builds. The runner materializes these at the start of
// Narrate; Perform's archive trigger uses them to know which build trees must cross to
// Narrate as transport artifacts.
func (c *Config) WorktreeOutputs() []WorktreeOutput {
	var out []WorktreeOutput
	for _, b := range c.Builds {
		for _, o := range b.Outputs {
			if o.LandsInWorktree() {
				out = append(out, WorktreeOutput{BuildID: b.ID, Output: o})
			}
		}
	}
	return out
}

// CrucibleConfig holds crucible-specific build configuration.
type CrucibleConfig struct {
	// ToolchainImage is the pinned container image for pass-2 verification.
	// e.g., "docker.io/library/golang:1.24-alpine"
	ToolchainImage string `yaml:"toolchain_image,omitempty"`
}

// StageConfig wires a binary build's output into a docker build's context. From names
// the binary build (its id); As is the destination path within the context, with
// {arch}/{os} placeholders substituted using Docker's naming (e.g. "app-{arch}" →
// "app-amd64") so a multi-arch copy-pre-built Dockerfile resolves per platform.
type StageConfig struct {
	From string `yaml:"from"`
	As   string `yaml:"as"`
}

// IsRequired returns whether build failure is a hard pipeline fail. Default: true.
func (b BuildConfig) IsRequired() bool {
	if b.Required != nil {
		return *b.Required
	}
	return true
}

// BuilderCommand returns the builder command, defaulting to "build".
func (b BuildConfig) BuilderCommand() string {
	if b.Command != "" {
		return string(b.Command)
	}
	return "build"
}

// OutputName returns the output artifact name, defaulting to basename of From.
func (b BuildConfig) OutputName() string {
	if b.Output != "" {
		return b.Output
	}
	if b.From != "" {
		// Strip trailing .go/.rs suffixes, then take basename
		from := b.From
		for _, suffix := range []string{".go", ".rs"} {
			from = strings.TrimSuffix(from, suffix)
		}
		return filepath.Base(from)
	}
	return b.ID
}
