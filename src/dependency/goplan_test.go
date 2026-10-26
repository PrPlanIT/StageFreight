package dependency

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// fakeGo stands in for provisioning: each requested version is a script that logs
// "version|GOTOOLCHAIN|args" and applies `go mod edit -go=` to the go.mod it runs beside.
func fakeGo(t *testing.T) (resolved *[]string, logPath string) {
	t.Helper()
	bin := t.TempDir()
	logPath = filepath.Join(bin, "calls.log")
	resolved = &[]string{}
	orig := resolveGo
	resolveGo = func(repoRoot, version string) (toolchain.Result, error) {
		*resolved = append(*resolved, version)
		path := filepath.Join(bin, "go-"+version)
		script := "#!/bin/sh\necho \"" + version + "|$GOTOOLCHAIN|$*\" >> " + logPath + "\n" +
			"if [ \"$1\" = mod ] && [ \"$2\" = edit ]; then sed -i \"s/^go .*/go ${3#-go=}/\" go.mod; fi\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return toolchain.Result{Tool: "go", Version: version, Path: path}, nil
	}
	t.Cleanup(func() { resolveGo = orig })
	return resolved, logPath
}

func TestPlanGoVersions(t *testing.T) {
	repo := t.TempDir()
	for _, d := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteFile(t, repo, "a/go.mod", "module a\n\ngo 1.27.1\n")
	mustWriteFile(t, repo, "b/go.mod", "module b\n\ngo 1.27.1\ntoolchain go1.27.5\n")
	sync := goDirectiveSyncResult{Targets: []goDirectiveSyncTarget{{ModuleDir: "a", GoVersion: "1.27.3"}, {ModuleDir: "b", GoVersion: "1.27.3"}}}

	got := planGoVersions(repo, []string{"a", "b"}, sync)
	if got["a"] != "1.27.3" {
		t.Errorf("a raised to its directive target: got %s, want 1.27.3", got["a"])
	}
	if got["b"] != "1.27.5" {
		t.Errorf("b declares a newer toolchain than the target: got %s, want 1.27.5", got["b"])
	}

	mustWriteFile(t, repo, "go.work", "go 1.27.0\n\nuse (\n\t./a\n\t./b\n)\n")
	got = planGoVersions(repo, []string{"a", "b"}, sync)
	if got["a"] != got["b"] {
		t.Errorf("a workspace runs every module under one Go, got %v", got)
	}
}

// TestProvisionGoPlan_OnePerVersion: modules sharing a version share one provisioned Go,
// and every runner pins GOTOOLCHAIN=local.
func TestProvisionGoPlan_OnePerVersion(t *testing.T) {
	resolved, logPath := fakeGo(t)
	plan, err := provisionGoPlan(t.TempDir(), map[string]string{"a": "1.27.3", "b": "1.27.3"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*resolved, []string{"1.27.3"}) {
		t.Errorf("provisioned %v, want 1.27.3 once", *resolved)
	}
	if _, err := plan.runner("b")(context.Background(), t.TempDir(), "version"); err != nil {
		t.Fatal(err)
	}
	if log, _ := os.ReadFile(logPath); !strings.Contains(string(log), "1.27.3|local|version") {
		t.Errorf("runner must pin GOTOOLCHAIN=local, log: %s", log)
	}
}

// TestUpdate_OneGoPerPass is the regression for a pass that staged go.mod's Go for
// module updates and a second, newer Go for the directive sync: with go.mod at 1.27.1
// behind a golang:1.27.3 builder, the whole pass provisions only 1.27.3, raises the
// directive first, runs the module update under the same Go, and commits go.mod.
func TestUpdate_OneGoPerPass(t *testing.T) {
	resolved, logPath := fakeGo(t)
	repo := initTestRepo(t, map[string]string{
		"go.mod":     "module example.com/x\n\ngo 1.27.1\n\nrequire example.com/y v1.0.0\n",
		"main.go":    "package main\n\nfunc main() {}\n",
		"Dockerfile": "FROM golang:1.27.3-alpine AS build\n",
	})
	deps := []supplychain.Dependency{
		{Name: "example.com/y", Current: "v1.0.0", Latest: "v1.1.0", Ecosystem: supplychain.EcosystemGoMod, File: "go.mod"},
		{Name: "golang", Current: "1.27.3-alpine", Latest: "1.27.3-alpine", Ecosystem: supplychain.EcosystemDockerImage, File: "Dockerfile"},
	}
	res, err := Update(context.Background(), UpdateConfig{RootDir: repo, OutputDir: t.TempDir()}, deps)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !slices.Equal(*resolved, []string{"1.27.3"}) {
		t.Fatalf("the pass must provision exactly one Go, 1.27.3; got %v", *resolved)
	}
	raw, _ := os.ReadFile(logPath)
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	edit, get := -1, -1
	for i, c := range calls {
		if !strings.HasPrefix(c, "1.27.3|local|") {
			t.Errorf("every go command must run under the planned, pinned Go: %q", c)
		}
		if strings.Contains(c, "mod edit -go=1.27.3") {
			edit = i
		}
		if strings.Contains(c, "get example.com/y@v1.1.0") {
			get = i
		}
	}
	if edit < 0 || get < 0 || edit > get {
		t.Errorf("the directive must be raised before the module update; calls: %v", calls)
	}
	if !slices.Contains(res.FilesChanged, "go.mod") {
		t.Errorf("a directive change must be in FilesChanged so it is committed, got %v", res.FilesChanged)
	}
}
