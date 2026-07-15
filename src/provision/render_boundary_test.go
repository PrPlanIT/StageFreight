package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderBoundary_LowLevelRenderStaysBehindStageBox enforces the terminal-output
// layering as a RATCHET. The blessed way to present provisioned tools is the single
// convention provision.StageBox(ctx, w, color) — call it once, in front of a phase's
// work box. The LOW-LEVEL provision.Render (arbitrary entries → box) is StageBox's
// implementation detail; calling it directly hand-assembles provisioning presentation
// and bypasses the convention, so it is fenced to a shrinking allowlist (see
// docs/design/plans/terminal-output-layering.md).
//
// A NEW direct Render caller outside the allowlist fails the build: use StageBox
// instead, or emit provision.Entry data. The list can only shrink, never grow —
// enforced by the build, not by convention. StageBox itself is unguarded: it IS the
// convention, part of the render vocabulary like output.NewSection.
func TestRenderBoundary_LowLevelRenderStaysBehindStageBox(t *testing.T) {
	// Package dirs (relative to src/) permitted to call the low-level provision.Render.
	// StageBox drains the ctx delta and calls Render internally (same package, invisible
	// to this grep), so phase renderers use StageBox and never appear here.
	allow := map[string]bool{
		"dependency": true, // GRANDFATHERED — renders its own one-off row; migrate to StageBox/data
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
			"return provision.Entry as data instead:\n  %s\nSee docs/design/plans/terminal-output-layering.md.",
			strings.Join(offenders, "\n  "))
	}
}
