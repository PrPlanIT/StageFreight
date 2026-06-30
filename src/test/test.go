package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/substrate"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// Request runs a set of already-resolved suites.
type Request struct {
	RootDir string
	Suites  []ResolvedSuite
	// Writer, if non-nil, streams each suite's output live (tee'd into the captured
	// SuiteResult.Output too). nil = capture only.
	Writer io.Writer
}

// Run realizes the native substrate the suites need (a C toolchain for `go -race`
// or Rust linking — via the existing substrate layer: apk-backed + cached in CI,
// ambient/noop locally, recorded as provenance), then executes each suite in its
// resolved working directory. Execution errors are recorded per-suite
// (Status=failed), never returned — the verdict lives in TestResult.
func Run(ctx context.Context, req Request) *TestResult {
	if needs := substrateNeeds(req.Suites); len(needs) > 0 {
		realized, err := substrate.NewRealizer(toolchain.SubstrateCacheDir()).Realize(ctx, needs)
		if err != nil && req.Writer != nil {
			// Non-fatal: suites needing it will fail with a clear compiler error.
			fmt.Fprintf(req.Writer, "  test: substrate realization warning: %v\n", err)
		}
		substrate.Report(reportWriter(req.Writer), realized)
	}

	res := &TestResult{}
	for _, s := range req.Suites {
		res.Suites = append(res.Suites, runSuite(ctx, req.RootDir, s, req.Writer))
	}
	return res
}

func runSuite(ctx context.Context, rootDir string, s ResolvedSuite, w io.Writer) SuiteResult {
	sr := SuiteResult{ID: s.ID, Type: s.Type, Gate: s.Gate}
	if len(s.Argv) == 0 {
		sr.Status = StatusSkipped
		return sr
	}

	// Resolve argv[0] to the toolchain's absolute path — go/cargo are provisioned by
	// the toolchain subsystem and invoked by absolute path, not via PATH. Build the
	// env per language (hermetic CleanEnv for go/rust + caches; full ambient for the
	// script escape hatch).
	argv := append([]string{}, s.Argv...)
	var env []string // nil ⇒ inherit parent env

	switch s.Type {
	case config.TestTypeScript:
		// Escape hatch: run with the full ambient environment (intentionally fewer
		// guarantees) so make/pytest/npm/etc. resolve normally.
		env = nil

	case config.TestTypeGo:
		goRes, err := toolchain.Resolve(rootDir, "go", toolchain.ResolveGoVersion(s.Dir, rootDir))
		if err != nil {
			return failSuite(sr, fmt.Errorf("resolving go toolchain: %w", err))
		}
		argv[0] = goRes.Path
		env = toolchain.CleanEnv()
		if gomod, gocache := toolchain.GoCacheDirs(); gomod != "" {
			env = append(env, "GOMODCACHE="+gomod, "GOCACHE="+gocache)
		}
		if hasFlag(argv, "-race") {
			// cgo: enable, and make the substrate-realized `cc` discoverable on PATH.
			env = append(env, "CGO_ENABLED=1", "PATH="+os.Getenv("PATH"))
		}

	case config.TestTypeRust:
		res, err := toolchain.Resolve(rootDir, "rust", toolchain.ResolveRustVersion(s.Dir, rootDir))
		if err != nil {
			return failSuite(sr, fmt.Errorf("resolving rust toolchain: %w", err))
		}
		argv[0] = res.Path
		env = toolchain.CleanEnv()
		if ch := toolchain.CargoCacheDir(); ch != "" {
			env = append(env, "CARGO_HOME="+ch)
		}
		// cargo invokes rustc (and the substrate-realized cc); put the toolchain bin
		// dir + ambient PATH in front (mirrors the Rust build engine).
		env = append(env, "PATH="+filepath.Dir(res.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = s.Dir
	if env != nil {
		cmd.Env = env
	}
	var buf bytes.Buffer
	if w != nil {
		cmd.Stdout = io.MultiWriter(w, &buf)
		cmd.Stderr = io.MultiWriter(w, &buf)
	} else {
		cmd.Stdout = &buf
		cmd.Stderr = &buf
	}
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

func failSuite(sr SuiteResult, err error) SuiteResult {
	sr.Status = StatusFailed
	sr.Err = err
	sr.Output = err.Error()
	return sr
}

// substrateNeeds derives the native capabilities the suites require — a C toolchain
// for `go test -race` (cgo) and Rust linking — as abstract capabilities (never
// packages), realized by the substrate backend. Deduped by capability+source.
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
		switch s.Type {
		case config.TestTypeGo:
			if hasFlag(s.Argv, "-race") {
				add(substrate.Need{Capability: "c-toolchain", Reason: "go-test-race-cgo", Source: "go test -race"})
			}
		case config.TestTypeRust:
			for _, n := range substrate.InferRustNeeds(s.Dir) {
				add(n)
			}
		}
	}
	return needs
}

func reportWriter(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return os.Stderr
}
