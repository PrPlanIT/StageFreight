package dependency

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	vulnscan "golang.org/x/vuln/scan"
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
	if runTests {
		var err error
		runGo, err = resolveGoRunner(repoRoot)
		if err != nil {
			return "", fmt.Errorf("go toolchain: %w", err)
		}
	}

	for _, dir := range dirs {
		if runTests {
			fmt.Fprintf(os.Stderr, "[deps:diag] verify: go test ./... start (%s)\n", dir)
			t0 := time.Now()
			testLog, err := runGoTest(ctx, dir, runGo)
			fmt.Fprintf(os.Stderr, "[deps:diag] verify: go test ./... done (%s, err=%v)\n", time.Since(t0).Round(time.Millisecond), err)
			log.WriteString(fmt.Sprintf("=== go test ./... (%s) ===\n", dir))
			log.WriteString(testLog)
			log.WriteString("\n")
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("go test failed in %s: %w", dir, err)
			}
		}

		if runVulncheck {
			fmt.Fprintf(os.Stderr, "[deps:diag] verify: govulncheck ./... start (%s)\n", dir)
			t0 := time.Now()
			vulnLog, err := runGovulncheck(ctx, dir)
			fmt.Fprintf(os.Stderr, "[deps:diag] verify: govulncheck ./... done (%s, err=%v)\n", time.Since(t0).Round(time.Millisecond), err)
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

func runGoTest(ctx context.Context, dir string, runGo goRunner) (string, error) {
	out, err := runGo(ctx, dir, "test", "./...")
	return string(out), err
}

// runGovulncheck runs govulncheck in-process against the module at dir.
// Uses the golang.org/x/vuln/scan library — no subprocess or Go toolchain required.
// The -C flag sets the working directory; no chdir needed.
func runGovulncheck(ctx context.Context, dir string) (string, error) {
	var buf bytes.Buffer
	cmd := vulnscan.Command(ctx, "-C", dir, "./...")
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	if err := cmd.Wait(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}
