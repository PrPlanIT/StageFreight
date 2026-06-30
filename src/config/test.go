package config

// Gate is the declarative lifecycle consequence of a test suite's failure.
//
// It is a typed policy value, decoded once at the config boundary so internal
// comparisons never use raw string literals. v1 implements `perform` (default)
// and `advisory`; `publish` is reserved (see the test-subsystem plan) — the
// "build locally but deny distribution" tier arrives with the finer
// authorizePhase rail when a suite actually needs it.
type Gate string

const (
	// GatePerform: a failed suite fails the audition exit code, which withholds
	// the cistate artifact and halts perform + everything downstream (only narrate
	// runs) — identical to how a lint critical gates today. This is the default.
	GatePerform Gate = "perform"
	// GateAdvisory: the suite runs and reports, but never affects the audition exit
	// code, so the pipeline ships regardless.
	GateAdvisory Gate = "advisory"
	// GatePublish: reserved (v2) — build the artifact but deny distribution.
	GatePublish Gate = "publish"
)

// TestType is the suite dialect — what the suite IS, not what executes it.
// `go`/`rust` are first-class typed (native-flag projection + builder-derived
// defaults); `script` is the intentionally-less-structured raw escape hatch.
// Future: `pytest`, `container`, `compose`.
type TestType string

const (
	TestTypeGo     TestType = "go"
	TestTypeRust   TestType = "rust"
	TestTypeScript TestType = "script"
)

// TestSuite is one behavioral-verification suite. Typed fields are declarative
// projections of the native flag a developer already types (`-race`, `-tags`,
// `--workspace`, `--features`), so the YAML reads as the command. Fields are
// type-specific — Go and Rust have different selection spaces and are NOT forced
// through one shared vocabulary. `Args` is the explicit, intentionally-unsafe
// escape hatch (bypasses the typed guarantees); `Command` (with `type: script`)
// replaces the runner entirely.
type TestSuite struct {
	ID      string   `yaml:"id"`
	Type    TestType `yaml:"type"`
	Gate    Gate     `yaml:"gate,omitempty"` // default: perform
	From    string   `yaml:"from,omitempty"` // module/crate dir when not at repo root (e.g. dd-ui's api/)
	Args    []string `yaml:"args,omitempty"` // raw passthrough escape hatch
	Command string   `yaml:"command,omitempty"`

	// ── Go (native `go test` flag projections) ──────────────────────────────
	Packages []string `yaml:"packages,omitempty"` // ./p/...   (default ./...)
	Tags     []string `yaml:"tags,omitempty"`     // -tags a,b
	Run      string   `yaml:"run,omitempty"`      // -run <regex>
	Timeout  string   `yaml:"timeout,omitempty"`  // -timeout <d>
	Race     *bool    `yaml:"race,omitempty"`     // -race
	Coverage *bool    `yaml:"coverage,omitempty"` // -coverprofile

	// ── Rust (native `cargo test` flag projections) ─────────────────────────
	Workspace *bool    `yaml:"workspace,omitempty"` // --workspace
	Features  []string `yaml:"features,omitempty"`  // --features a,b
	Tests     []string `yaml:"tests,omitempty"`     // --test <name>
	Release   *bool    `yaml:"release,omitempty"`   // --release
	Nextest   *bool    `yaml:"nextest,omitempty"`   // cargo nextest run
}

// EffectiveGate returns the suite's gate, defaulting to perform (the strict,
// zero-wiring tier) when unset.
func (s TestSuite) EffectiveGate() Gate {
	if s.Gate == "" {
		return GatePerform
	}
	return s.Gate
}

// TestConfig is the test subsystem. Auto-on by default: when Enabled and no
// suites are declared, a default suite is synthesized per builder from build
// metadata (unless Auto is explicitly false).
type TestConfig struct {
	Preset  string      `yaml:"preset,omitempty"`
	Enabled bool        `yaml:"enabled"`
	Auto    *bool       `yaml:"auto,omitempty"` // nil ⇒ true
	Suites  []TestSuite `yaml:"suites,omitempty"`
}

// DefaultTestConfig is auto-on with no declared suites (synthesis derives the
// default suite from the configured builds).
func DefaultTestConfig() TestConfig {
	return TestConfig{Enabled: true}
}

// AutoSynthesize reports whether a default suite should be synthesized from build
// metadata when the operator declared none. Defaults to true.
func (t TestConfig) AutoSynthesize() bool {
	if t.Auto != nil {
		return *t.Auto
	}
	return true
}
