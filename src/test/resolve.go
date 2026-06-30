package test

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// ResolvedSuite is a suite ready to execute: a concrete argv + working directory.
type ResolvedSuite struct {
	ID          string
	Type        config.TestType
	Gate        config.Gate
	Argv        []string
	Dir         string
	Synthesized bool
	Provenance  string // human note (synthesized suites): why / from which build
}

// Resolve turns the test config (+ builds) into executable suites. When the
// operator declared no suites and auto-synthesis is on, it derives one default
// suite per testable (go/rust binary) build, each with a provenance-stamped id.
func Resolve(cfg *config.Config, rootDir string) ([]ResolvedSuite, error) {
	tc := cfg.Test
	if !tc.Enabled {
		return nil, nil
	}
	if len(tc.Suites) == 0 {
		if !tc.AutoSynthesize() {
			return nil, nil
		}
		return synthesize(cfg, rootDir), nil
	}
	seenID := map[string]bool{}
	out := make([]ResolvedSuite, 0, len(tc.Suites))
	for _, s := range tc.Suites {
		if strings.TrimSpace(s.ID) == "" {
			return nil, fmt.Errorf("test suite missing id")
		}
		if seenID[s.ID] {
			return nil, fmt.Errorf("duplicate test suite id %q", s.ID)
		}
		seenID[s.ID] = true
		rs, err := resolveSuite(s, rootDir)
		if err != nil {
			return nil, err
		}
		out = append(out, rs)
	}
	return out, nil
}

// synthesize derives one default suite per go/rust binary build. The default is
// the plain base command (`go test ./...` / `cargo test [--workspace]`) — no
// `-race` (it needs CGO/gcc, so it stays opt-in per suite for fleet portability).
func synthesize(cfg *config.Config, rootDir string) []ResolvedSuite {
	var out []ResolvedSuite
	seen := map[string]bool{}
	for _, b := range cfg.Builds {
		if b.Kind != "binary" {
			continue
		}
		builder := b.Builder
		if builder == "" {
			builder = "go"
		}
		if builder != "go" && builder != "rust" {
			continue
		}
		base, dir, err := build.DefaultTestCommand(builder, b.From, rootDir)
		if err != nil {
			continue
		}
		key := builder + "\x00" + dir
		if seen[key] {
			continue // multiple builds sharing a module root → one suite
		}
		seen[key] = true
		out = append(out, ResolvedSuite{
			ID:          fmt.Sprintf("%s-default-%s", builder, b.ID),
			Type:        config.TestType(builder),
			Gate:        config.GatePerform,
			Argv:        append([]string{}, base...),
			Dir:         dir,
			Synthesized: true,
			Provenance:  fmt.Sprintf("synthesized from build %q (%s, module %s)", b.ID, builder, relTo(rootDir, dir)),
		})
	}
	return out
}

func resolveSuite(s config.TestSuite, rootDir string) (ResolvedSuite, error) {
	rs := ResolvedSuite{ID: s.ID, Type: s.Type, Gate: s.EffectiveGate(), Dir: rootDir}
	switch s.Type {
	case config.TestTypeScript:
		if strings.TrimSpace(s.Command) == "" {
			return rs, fmt.Errorf("test suite %q: type %q requires a command", s.ID, s.Type)
		}
		if s.From != "" {
			rs.Dir = filepath.Join(rootDir, s.From)
		}
		rs.Argv = []string{"sh", "-c", s.Command}
		return rs, nil
	case config.TestTypeGo:
		_, dir, err := build.DefaultTestCommand("go", s.From, rootDir)
		if err != nil {
			return rs, fmt.Errorf("test suite %q: %w", s.ID, err)
		}
		rs.Dir = dir
		rs.Argv = goArgv(s)
		return rs, nil
	case config.TestTypeRust:
		base, dir, err := build.DefaultTestCommand("rust", s.From, rootDir)
		if err != nil {
			return rs, fmt.Errorf("test suite %q: %w", s.ID, err)
		}
		rs.Dir = dir
		rs.Argv = rustArgv(s, hasFlag(base, "--workspace"))
		return rs, nil
	default:
		return rs, fmt.Errorf("test suite %q: unknown type %q (supported: go, rust, script)", s.ID, s.Type)
	}
}

// goArgv builds `go test [flags] [packages|./...] [args]`.
func goArgv(s config.TestSuite) []string {
	argv := []string{"go", "test"}
	if len(s.Tags) > 0 {
		argv = append(argv, "-tags", strings.Join(s.Tags, ","))
	}
	if s.Run != "" {
		argv = append(argv, "-run", s.Run)
	}
	if s.Timeout != "" {
		argv = append(argv, "-timeout", s.Timeout)
	}
	if boolVal(s.Race) {
		argv = append(argv, "-race")
	}
	if boolVal(s.Coverage) {
		argv = append(argv, "-cover")
	}
	pkgs := s.Packages
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	argv = append(argv, pkgs...)
	argv = append(argv, s.Args...)
	return argv
}

// rustArgv builds `cargo (test|nextest run) [--workspace] [--release] [--features f]…
// [--test t]… [args]`.
func rustArgv(s config.TestSuite, workspaceFromManifest bool) []string {
	var argv []string
	if boolVal(s.Nextest) {
		argv = []string{"cargo", "nextest", "run"}
	} else {
		argv = []string{"cargo", "test"}
	}
	if boolVal(s.Workspace) || workspaceFromManifest {
		argv = append(argv, "--workspace")
	}
	if boolVal(s.Release) {
		argv = append(argv, "--release")
	}
	for _, f := range s.Features {
		argv = append(argv, "--features", f)
	}
	for _, t := range s.Tests {
		argv = append(argv, "--test", t)
	}
	argv = append(argv, s.Args...)
	return argv
}

func hasFlag(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}

func boolVal(b *bool) bool { return b != nil && *b }

func relTo(root, dir string) string {
	if r, err := filepath.Rel(root, dir); err == nil {
		return r
	}
	return dir
}
