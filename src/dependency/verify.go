package dependency

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Verify runs post-update verification (go test + govulncheck) on the
// given module directories. moduleDirs should be absolute paths — only
// dirs where updates were actually applied.
func Verify(ctx context.Context, moduleDirs []string, repoRoot string, runTests, runVulncheck bool) (string, error) {
	var log strings.Builder
	var firstErr error

	if len(moduleDirs) == 0 {
		log.WriteString("no Go modules updated; verification skipped\n")
		return log.String(), nil
	}

	// Deduplicate and sort for deterministic log output
	seen := make(map[string]struct{}, len(moduleDirs))
	var dirs []string
	for _, d := range moduleDirs {
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)

	var runGo goRunner
	if runTests || runVulncheck {
		var err error
		runGo, err = resolveGoRunner(repoRoot)
		if err != nil {
			return "", fmt.Errorf("go toolchain: %w", err)
		}
	}

	for _, dir := range dirs {
		// A go.mod can exist purely for tooling — e.g. a Hugo theme module —
		// with no Go packages. `go test ./...` / govulncheck on such a module are
		// meaningless and exit non-zero; skip verification rather than fail the
		// update. (Detected by walking for any .go source, so it doesn't depend on
		// `go list` exit semantics.)
		if !moduleHasGoFiles(dir) {
			log.WriteString(fmt.Sprintf("=== %s: no Go packages (tooling/content module); verification skipped ===\n", dir))
			continue
		}

		if runTests {
			testLog, err := runGoTest(ctx, dir, runGo)
			log.WriteString(fmt.Sprintf("=== go test ./... (%s) ===\n", dir))
			log.WriteString(testLog)
			log.WriteString("\n")
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("go test failed in %s: %w", dir, err)
			}
		}

		if runVulncheck {
			vulnLog, err := runGovulncheck(ctx, dir, runGo)
			if vulnLog != "" {
				log.WriteString(fmt.Sprintf("=== govulncheck ./... (%s) ===\n", dir))
				log.WriteString(vulnLog)
				log.WriteString("\n")
			}
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("govulncheck failed in %s: %w", dir, err)
			}
		}
	}

	return log.String(), firstErr
}

// moduleHasGoFiles reports whether a module directory contains any Go source —
// i.e. whether there is anything for `go test` / govulncheck to act on. A module
// with a go.mod but no .go files (a content/theme module) is not a Go application.
func moduleHasGoFiles(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Skip nested vendor dirs and dotdirs (e.g. .git) — not module source.
			if path != dir {
				if name := d.Name(); name == "vendor" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func runGoTest(ctx context.Context, dir string, runGo goRunner) (string, error) {
	out, err := runGo(ctx, dir, "test", "./...")
	return string(out), err
}

const govulncheckModule = "golang.org/x/vuln/cmd/govulncheck@latest"

func runGovulncheck(ctx context.Context, dir string, runGo goRunner) (string, error) {
	out, err := runGo(ctx, dir, "run", govulncheckModule, "./...")
	return string(out), err
}
