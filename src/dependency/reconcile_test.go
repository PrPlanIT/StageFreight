package dependency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

var golangTags = []string{
	"1.24", "1.25", "1.24.13", "1.25.0", "1.25.7", "1.26.5",
	"1.24.13-alpine3.20", "1.25.7-alpine3.20",
}

// writeRepo lays down a go.mod + Dockerfile in a temp dir and returns the root.
func writeRepo(t *testing.T, goDirective, dockerfile string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module example.com/x\n\ngo "+goDirective+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func builderDep(tags []string) supplychain.Dependency {
	return supplychain.Dependency{
		Name:              "golang:1.24.13",
		Current:           "1.24.13",
		Ecosystem:         supplychain.EcosystemDockerImage,
		File:              "Dockerfile",
		Line:              1,
		AvailableVersions: tags,
	}
}

// TestReconcileRepository_EchoipScenario reproduces the exact break: a go.mod that
// tidy raised to `go 1.25.0` while the builder was pinned at golang:1.24.13.
// Reconciliation must bump the builder to the minimal satisfying tag, land it on
// disk, and record it for the commit — with no config errors.
func TestReconcileRepository_EchoipScenario(t *testing.T) {
	root := writeRepo(t, "1.25.0", "FROM golang:1.24.13 AS build\nRUN make\n")
	result := &UpdateResult{}

	reconcileRepository(root, []supplychain.Dependency{builderDep(golangTags)}, result, false)

	if len(result.ReconcileErrors) != 0 {
		t.Fatalf("unexpected config errors: %+v", result.ReconcileErrors)
	}
	got, _ := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if want := "FROM golang:1.25.7 AS build"; !strings.Contains(string(got), want) {
		t.Fatalf("Dockerfile not reconciled:\n%s\nwant line %q", got, want)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("want 1 applied reconciliation, got %d", len(result.Applied))
	}
	if !contains(result.FilesChanged, "Dockerfile") {
		t.Fatalf("Dockerfile not recorded in FilesChanged: %v", result.FilesChanged)
	}
}

// TestReconcileRepository_DryRunFailsLoudButDoesNotWrite: evaluate mode must NOT
// mutate the working tree, but it must SURFACE the detected inconsistency as a
// run-failing finding (naming the derived fix) rather than passing silently and
// letting the build crash later at a cryptic toolchain error.
func TestReconcileRepository_DryRunFailsLoudButDoesNotWrite(t *testing.T) {
	root := writeRepo(t, "1.25.0", "FROM golang:1.24.13 AS build\n")
	result := &UpdateResult{}

	reconcileRepository(root, []supplychain.Dependency{builderDep(golangTags)}, result, true)

	got, _ := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if strings.Contains(string(got), "1.25.7") {
		t.Fatalf("dry-run must not rewrite the Dockerfile:\n%s", got)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("dry-run must not record applied changes, got %d", len(result.Applied))
	}
	if len(result.ReconcileErrors) != 1 {
		t.Fatalf("dry-run must surface the detected reconciliation as a finding, got %d", len(result.ReconcileErrors))
	}
	if msg := result.ReconcileErrors[0].Message; !strings.Contains(msg, "golang:1.25.7") {
		t.Fatalf("dry-run finding should name the derived fix, got %q", msg)
	}
}

// TestReconcileRepository_Unsatisfiable: the operator's exact variant is not
// published on the floor's minor line. Reconciliation must NOT drift to another
// variant or overshoot — it must surface a config error and leave the file alone.
func TestReconcileRepository_UnsatisfiableVariantIsConfigError(t *testing.T) {
	root := writeRepo(t, "1.25.0", "FROM golang:1.24.13-alpine3.18 AS build\n")
	dep := builderDep(golangTags)
	dep.Name = "golang:1.24.13-alpine3.18"
	dep.Current = "1.24.13-alpine3.18"
	result := &UpdateResult{}

	reconcileRepository(root, []supplychain.Dependency{dep}, result, false)

	if len(result.ReconcileErrors) != 1 {
		t.Fatalf("want 1 config error, got %d (applied=%d)", len(result.ReconcileErrors), len(result.Applied))
	}
	got, _ := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if !strings.Contains(string(got), "1.24.13-alpine3.18") {
		t.Fatalf("Dockerfile must be untouched on config error:\n%s", got)
	}
}

// TestReconcileRepository_AlreadySatisfied: builder already meets the floor — a
// no-op with no mutation and no error.
func TestReconcileRepository_AlreadySatisfied(t *testing.T) {
	root := writeRepo(t, "1.24.0", "FROM golang:1.24.13 AS build\n")
	result := &UpdateResult{}

	reconcileRepository(root, []supplychain.Dependency{builderDep(golangTags)}, result, false)

	if len(result.Applied) != 0 || len(result.ReconcileErrors) != 0 {
		t.Fatalf("want no-op, got applied=%d errors=%d", len(result.Applied), len(result.ReconcileErrors))
	}
}
