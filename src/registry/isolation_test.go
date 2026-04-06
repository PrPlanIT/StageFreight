package registry

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestNewRegistryOnlyConsumesResolvedValues is a structural invariant test
// that enforces the single-resolution-path rule for registry client
// construction.
//
// The rule: every call to registry.NewRegistry MUST receive its arguments
// from a resolved value — either a *config.ResolvedRegistry (produced by
// config.ResolveRegistryForTarget) or a build.RegistryTarget (produced at
// plan time, which itself uses ResolveRegistryForTarget under the hood).
//
// It is FORBIDDEN to pass raw TargetConfig fields (t.Provider, t.URL,
// t.Credentials) directly to registry.NewRegistry. That pattern bypasses
// the identity graph and silently breaks for targets that use
// `registry: <id>` references instead of inline url/path fields.
//
// If this test fails, someone has reintroduced an inline-field shortcut.
// Fix the caller to go through config.ResolveRegistryForTarget first; do
// NOT silence this test by adding the forbidden receiver name to the
// allowlist.
//
// The allowlist below is intentionally tiny. Each entry has a comment
// explaining why it's a resolved value. Adding a new entry requires
// understanding WHY the new receiver holds resolved data.
func TestNewRegistryOnlyConsumesResolvedValues(t *testing.T) {
	// Allowlist: receiver identifier names that are known to hold either
	// *config.ResolvedRegistry or build.RegistryTarget. Any other receiver
	// used as a registry.NewRegistry argument fails the test.
	allowedReceivers := map[string]string{
		"reg":         "build.RegistryTarget (plan-time) or *config.ResolvedRegistry (resolver)",
		"resolved":    "*config.ResolvedRegistry from config.ResolveRegistryForTarget",
		"resolvedReg": "*config.ResolvedRegistry from config.ResolveRegistryForTarget",
	}

	// Forbidden receivers: known TargetConfig variable names. Explicit
	// denylist makes failures clearer than "not in allowlist".
	forbiddenReceivers := map[string]bool{
		"t":         true, // TargetConfig loop variable
		"tc":        true, // TargetConfig loop variable (manifest/generate.go style)
		"tgt":       true, // alternate TargetConfig variable
		"target":    true, // full-name TargetConfig variable
		"targetCfg": true, // TargetConfig pointer variable
	}

	// Identity fields that must not be read from raw TargetConfig.
	identityFields := map[string]bool{
		"Provider":    true,
		"URL":         true,
		"Credentials": true,
	}

	srcRoot := findSrcRoot(t)

	var violations []string

	err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip vendored code and generated output directories if they exist.
			base := info.Name()
			if base == "vendor" || base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip this test file itself — it names the forbidden tokens in
		// comments and string literals.
		if strings.HasSuffix(path, "isolation_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			// Don't fail the test on unparseable files — they're usually
			// work-in-progress and will be caught by `go build` anyway.
			return nil
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkgIdent.Name != "registry" || sel.Sel.Name != "NewRegistry" {
				return true
			}

			// Found a registry.NewRegistry(...) call. Check each argument
			// for the forbidden pattern: a selector expression whose base
			// is a known TargetConfig variable name AND whose field is an
			// identity field.
			for i, arg := range call.Args {
				argSel, ok := arg.(*ast.SelectorExpr)
				if !ok {
					continue // literal, function call, etc. — not a field access
				}
				base, ok := argSel.X.(*ast.Ident)
				if !ok {
					continue // not a simple identifier receiver
				}

				fieldName := argSel.Sel.Name
				if !identityFields[fieldName] {
					continue // not an identity field — irrelevant
				}

				// Forbidden: TargetConfig receiver with identity field.
				if forbiddenReceivers[base.Name] {
					pos := fset.Position(call.Pos())
					violations = append(violations, fmt.Sprintf(
						"%s:%d: registry.NewRegistry arg %d reads %s.%s (raw TargetConfig field — use config.ResolveRegistryForTarget)",
						relPath(pos.Filename, srcRoot), pos.Line, i, base.Name, fieldName))
					continue
				}

				// Not on the allowlist: warn loudly. The allowlist is
				// intentionally small; adding a new name requires
				// understanding why it holds resolved data.
				if _, ok := allowedReceivers[base.Name]; !ok {
					pos := fset.Position(call.Pos())
					violations = append(violations, fmt.Sprintf(
						"%s:%d: registry.NewRegistry arg %d reads %s.%s (receiver %q not in allowlist — prove it holds a resolved value and add it to the test allowlist with a justifying comment)",
						relPath(pos.Filename, srcRoot), pos.Line, i, base.Name, fieldName, base.Name))
				}
			}
			return true
		})

		return nil
	})

	if err != nil {
		t.Fatalf("walking src root %s: %v", srcRoot, err)
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf(
			"INVARIANT VIOLATION: registry.NewRegistry must only consume resolved registry values.\n\n"+
				"The identity graph (config.ResolveRegistryForTarget → *config.ResolvedRegistry,\n"+
				"or image_engine.planDockerBuild → build.RegistryTarget) is the ONLY authorized\n"+
				"path from target configuration to runtime registry client.\n\n"+
				"Violations:\n  %s\n\n"+
				"Fix: route the caller through config.ResolveRegistryForTarget and read from\n"+
				"the returned *config.ResolvedRegistry. Do NOT silence this test by adding a\n"+
				"forbidden receiver name to the allowlist — that defeats the invariant.",
			strings.Join(violations, "\n  "))
	}
}

// findSrcRoot locates the src/ directory by walking up from this test
// file's location. Using runtime.Caller keeps the test portable across
// test runners that may set different working directories.
func findSrcRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot locate test file path")
	}
	// thisFile = .../src/registry/isolation_test.go
	// srcRoot  = .../src
	return filepath.Dir(filepath.Dir(thisFile))
}

// relPath returns a path relative to srcRoot, for cleaner violation
// messages. Falls back to the absolute path on failure.
func relPath(abs, srcRoot string) string {
	rel, err := filepath.Rel(srcRoot, abs)
	if err != nil {
		return abs
	}
	return rel
}
