package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoDirectWhenAccessOutsideConfig enforces the eligibility-routing invariant
// (invariants.md #7): outside src/config, code may ask whether a target matches
// (config.TargetMatches / TargetMatchesEnv) or whether it is unconditional
// (config.TargetIsUnconditional), but may NOT read When.Events / When.Branches /
// When.GitTags directly. There is exactly one interpreter of target constraints —
// this package. This tripwire fails the build if a parallel interpreter (the next
// targetAllowed) reappears.
//
// House style (see TestNoIdentityReconstructionPatterns): a regex tripwire, not
// an absence proof — but every parallel matcher takes the same shape (a direct
// When.<field> selector), so the obvious regression is caught at PR time.
func TestNoDirectWhenAccessOutsideConfig(t *testing.T) {
	whenAccess := regexp.MustCompile(`\.When\.(Events|Branches|GitTags)\b`)
	var violations []string

	// CWD during `go test` is this package dir (src/config); ".." is the src tree.
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// src/config is the sanctioned home for when:-interpretation.
			if d.Name() == "config" && filepath.Dir(path) == ".." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		s := string(src)
		for _, loc := range whenAccess.FindAllStringIndex(s, -1) {
			line := 1 + strings.Count(s[:loc[0]], "\n")
			violations = append(violations, fmt.Sprintf("%s:%d  %s", filepath.Clean(path), line, s[loc[0]:loc[1]]))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk src tree: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("direct When.* access outside src/config — route through "+
			"config.TargetMatches / TargetMatchesEnv / TargetIsUnconditional instead:\n  %s",
			strings.Join(violations, "\n  "))
	}
}
