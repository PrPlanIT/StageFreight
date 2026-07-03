package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderBoundary_OnlyPresentationLayerCallsRender enforces the terminal-output
// layering as a RATCHET. provision.Render is PRESENTATION; domain packages must emit
// their tools as data (provision.Entry, via the pure FromToolchain/FromSubstrate
// mappers) and let the cli/cmd layer render. Only cli/cmd — plus a shrinking allowlist
// of packages grandfathered pending the migration in
// docs/architecture/terminal-output-layering.md — may call provision.Render.
//
// A NEW caller outside the allowlist fails the build. That freezes the divergence: it
// can only shrink (as grandfathered packages migrate and drop off the list), never
// grow. Enforced by the build, not by convention — the same posture ci-render uses.
func TestRenderBoundary_OnlyPresentationLayerCallsRender(t *testing.T) {
	// Package dirs (relative to src/) permitted to call provision.Render.
	allow := map[string]bool{
		"cli/cmd":    true, // the presentation layer — where rendering belongs
		"dependency": true, // GRANDFATHERED — migrate to data + cli/cmd render
	}

	const srcRoot = ".." // `go test` runs in src/provision; .. == src

	var offenders []string
	err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "provision.Render(") {
			return nil
		}
		rel, err := filepath.Rel(srcRoot, filepath.Dir(path))
		if err != nil {
			return err
		}
		if pkg := filepath.ToSlash(rel); !allow[pkg] {
			offenders = append(offenders, pkg+" ("+filepath.Base(path)+")")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking src: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("provision.Render is presentation and may only be called from cli/cmd "+
			"(plus the grandfathered allowlist).\nNew offender(s) — move rendering to cli/cmd and "+
			"return provision.Entry as data instead:\n  %s\nSee docs/architecture/terminal-output-layering.md.",
			strings.Join(offenders, "\n  "))
	}
}
