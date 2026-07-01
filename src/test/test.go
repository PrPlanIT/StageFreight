package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/substrate"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// realizeSubstrate provisions the native capabilities the suites need (git for
// tests that exec git, a C toolchain for cgo/-race) — TEST TIME only, the shipped
// image stays minimal. Returns the realized outcomes for structured reporting.
func realizeSubstrate(ctx context.Context, suites []ResolvedSuite) []substrate.Realized {
	needs := substrateNeeds(suites)
	if len(needs) == 0 {
		return nil
	}
	realized, _ := substrate.NewRealizer(toolchain.SubstrateCacheDir()).Realize(ctx, needs)
	return realized
}

// runSuite runs one resolved suite in its working directory. onPkg (nil-safe) fires
// per package as it completes — so the renderer can stream rows live INTO its
// section. Execution errors are recorded on the result (Status=failed), never
// returned; the verdict lives in the SuiteResult.
func runSuite(ctx context.Context, rootDir string, s ResolvedSuite, onPkg func(PackageResult)) SuiteResult {
	sr := SuiteResult{ID: s.ID, Tool: s.Tool, Gate: s.Gate}
	if len(s.Argv) == 0 {
		sr.Status = StatusSkipped
		return sr
	}

	argv := append([]string{}, s.Argv...)
	var env []string // nil ⇒ inherit parent env

	switch s.Tool {
	case config.TestToolScript:
		// Escape hatch: run with the full ambient environment.
		env = nil

	case config.TestToolGo:
		goRes, err := toolchain.Resolve(rootDir, "go", toolchain.ResolveGoVersion(s.Dir, rootDir))
		if err != nil {
			return failSuite(sr, fmt.Errorf("resolving go toolchain: %w", err))
		}
		argv[0] = goRes.Path
		env = toolchain.CleanEnv()
		if gomod, gocache := toolchain.GoCacheDirs(); gomod != "" {
			env = append(env, "GOMODCACHE="+gomod, "GOCACHE="+gocache)
		}
		// Go tests shell out (git, cc) — give them a real PATH (a hermetic build needs
		// none; a test suite does), replacing CleanEnv's empty one.
		env = setEnv(env, "PATH", os.Getenv("PATH"))
		if hasFlag(argv, "-race") {
			env = setEnv(env, "CGO_ENABLED", "1")
		}
		return runGoSuite(ctx, sr, goRes.Path, s, argv, env, onPkg)

	case config.TestToolRust:
		res, err := toolchain.Resolve(rootDir, "rust", toolchain.ResolveRustVersion(s.Dir, rootDir))
		if err != nil {
			return failSuite(sr, fmt.Errorf("resolving rust toolchain: %w", err))
		}
		argv[0] = res.Path
		env = toolchain.CleanEnv()
		if ch := toolchain.CargoCacheDir(); ch != "" {
			env = append(env, "CARGO_HOME="+ch)
		}
		env = setEnv(env, "PATH", filepath.Dir(res.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
		return runRustSuite(ctx, sr, s, argv, env, onPkg)
	}

	// Generic exec (script escape hatch) — no per-unit projection, capture + record.
	start := time.Now()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = s.Dir
	if env != nil {
		cmd.Env = env
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	sr.Duration = time.Since(start)
	sr.Output = buf.String()
	if err != nil {
		sr.Status = StatusFailed
		sr.Err = err
	} else {
		sr.Status = StatusPassed
	}
	return sr
}

// runRustSuite runs a Rust suite via `cargo test`, parsing the merged output into
// per-test-binary results and streaming onPkg as each finishes. cargo prints
// "Running <binary>" to stderr and the libtest results to stdout, so both are merged
// into one ordered stream. A compile failure (nothing parsed + non-zero exit) is
// surfaced as a synthetic failure so the section still says why.
func runRustSuite(ctx context.Context, sr SuiteResult, s ResolvedSuite, argv, env []string, onPkg func(PackageResult)) SuiteResult {
	start := time.Now()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = s.Dir
	cmd.Env = env
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw // exec serializes writes when Stdout==Stderr
	if err := cmd.Start(); err != nil {
		return failSuite(sr, fmt.Errorf("starting cargo test: %w", err))
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait(); pw.Close() }()

	var raw bytes.Buffer
	pkgs := parseCargoTest(io.TeeReader(pr, &raw), onPkg)
	err := <-waitErr

	sr.Duration = time.Since(start)
	sr.Packages = pkgs
	sr.Output = raw.String()
	if err != nil {
		sr.Status = StatusFailed
		sr.Err = err
		if len(pkgs) == 0 {
			// compile error / unrecognized output — keep the section honest.
			fp := PackageResult{Rel: "cargo test", Status: StatusFailed, Coverage: -1,
				Failures: []TestFailure{{Name: "(build/run)", Output: raw.String()}}}
			sr.Packages = []PackageResult{fp}
			if onPkg != nil {
				onPkg(fp)
			}
		}
	} else {
		sr.Status = StatusPassed
	}
	return sr
}

func failSuite(sr SuiteResult, err error) SuiteResult {
	sr.Status = StatusFailed
	sr.Err = err
	sr.Output = err.Error()
	return sr
}

// runGoSuite runs a Go suite via `go test -json`, parsing the transport stream into
// per-package results (with doc synopses) and firing onPkg as each package finishes.
// Build/exec failures still set Status=failed via the exit code.
func runGoSuite(ctx context.Context, sr SuiteResult, goBin string, s ResolvedSuite, argv, env []string, onPkg func(PackageResult)) SuiteResult {
	sr.CoverageMin, sr.Coverage = s.CoverageMin, -1
	modulePath := goModulePath(goBin, s.Dir, env)
	synopses := goSynopses(goBin, s.Dir, env)

	// With coverage on, capture a profile so we can report the STATEMENT-WEIGHTED
	// suite total (go tool cover -func) instead of a misleading per-package average.
	var profile string
	if hasFlag(argv, "-cover") {
		if f, err := os.CreateTemp("", "sf-cover-*.out"); err == nil {
			profile = f.Name()
			f.Close()
			defer os.Remove(profile)
			argv = replaceFlag(argv, "-cover", "-coverprofile="+profile)
		}
	}

	jargv := injectAfter(argv, 1, "-json") // argv[1] == "test"

	start := time.Now()
	cmd := exec.CommandContext(ctx, jargv[0], jargv[1:]...)
	cmd.Dir = s.Dir
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failSuite(sr, fmt.Errorf("go test stdout pipe: %w", err))
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return failSuite(sr, fmt.Errorf("starting go test: %w", err))
	}
	pkgs := parseGoTest(stdout, modulePath, synopses, onPkg)
	err = cmd.Wait()

	sr.Duration = time.Since(start)
	sr.Packages = pkgs
	sr.Output = errBuf.String()
	if err != nil {
		sr.Status = StatusFailed
		sr.Err = err
	} else {
		sr.Status = StatusPassed
	}

	// Statement-weighted coverage total + threshold gate: coverage below the bar
	// fails the suite (gates the pipeline) even when every test passed.
	if profile != "" {
		if total, ok := goCoverageTotal(goBin, profile, env); ok {
			sr.Coverage = total
			if sr.CoverageMin > 0 && total < sr.CoverageMin && sr.Status != StatusFailed {
				sr.Status = StatusFailed
				sr.Err = fmt.Errorf("coverage %.1f%% below minimum %.1f%%", total, sr.CoverageMin)
			}
		}
	}
	return sr
}

// substrateNeeds derives the native capabilities the suites require — git for Go
// tests (repo fixtures, mirror clones, system-git transport) and a C toolchain for
// `go test -race` / Rust linking — as abstract capabilities, deduped by cap+source.
func substrateNeeds(suites []ResolvedSuite) []substrate.Need {
	var needs []substrate.Need
	seen := map[string]bool{}
	add := func(n substrate.Need) {
		k := n.Capability + "|" + n.Source
		if !seen[k] {
			seen[k] = true
			needs = append(needs, n)
		}
	}
	for _, s := range suites {
		switch s.Tool {
		case config.TestToolGo:
			add(substrate.Need{Capability: "git", Reason: "go-tests-exec-git", Source: "go test"})
			if hasFlag(s.Argv, "-race") {
				add(substrate.Need{Capability: "c-toolchain", Reason: "go-test-race-cgo", Source: "go test -race"})
			}
		case config.TestToolRust:
			for _, n := range substrate.InferRustNeeds(s.Dir) {
				add(n)
			}
		}
	}
	return needs
}

// setEnv replaces (or appends) key=val in env, so the child never sees a duplicate
// key (whose resolution is unspecified). Used to override CleanEnv's empty PATH.
func setEnv(env []string, key, val string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}
