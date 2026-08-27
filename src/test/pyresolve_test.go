package test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// pytestArgv projects the shared selection vocabulary onto pytest's native flags:
// Run → -k, Markers → -m, Coverage → --cov, Packages → positional paths. The
// --junitxml transport is the runner's, never the config's.
func TestPytestArgv(t *testing.T) {
	tr := true
	got := pytestArgv(config.TestSuite{
		ID: "unit", Tool: config.TestToolPython,
		Run:      "login",
		Markers:  []string{"not slow"},
		Coverage: &tr,
		Packages: []string{"tests/"},
		Args:     []string{"-x"},
	})
	want := []string{"python", "-m", "pytest", "-k", "login", "-m", "not slow",
		"--cov", "--cov-report=term", "tests/", "-x"}
	if len(got) != len(want) {
		t.Fatalf("argv = %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %v\nwant %v", got, want)
		}
	}
	for _, a := range got {
		if a == "--junitxml" || len(a) > 10 && a[:10] == "--junitxml" {
			t.Error("transport flag must be owned by the runner, not the argv builder")
		}
	}
}

// A bare suite is just `python -m pytest` — pytest's own rootdir discovery is the
// ./... equivalent, so no paths are invented.
func TestPytestArgv_Defaults(t *testing.T) {
	got := pytestArgv(config.TestSuite{ID: "unit", Tool: config.TestToolPython})
	if len(got) != 3 || got[0] != "python" || got[2] != "pytest" {
		t.Errorf("bare argv = %v, want [python -m pytest]", got)
	}
}

// The suite resolves its working directory to the PROJECT MANIFEST root (where the
// venv and pytest's rootdir belong), the same way go roots at go.mod.
func TestResolveSuite_PythonRootsAtManifest(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "svc")
	if err := os.MkdirAll(filepath.Join(sub, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "pyproject.toml"), []byte("[project]\nname='x'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := resolveSuite(config.TestSuite{ID: "unit", Tool: config.TestToolPython, From: "svc"}, root)
	if err != nil {
		t.Fatalf("resolveSuite: %v", err)
	}
	if rs.Dir != sub {
		t.Errorf("Dir = %q, want the manifest root %q", rs.Dir, sub)
	}
}

// The venv key is CONTENT-addressed: editing a dependency manifest yields a new
// environment, an untouched project reuses the warm one.
func TestPyVenvKey_ContentAddressed(t *testing.T) {
	dir := t.TempDir()
	req := filepath.Join(dir, "requirements.txt")
	if err := os.WriteFile(req, []byte("requests==2.34.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k1, m1 := pyVenvKey("/usr/bin/python3", dir, false)
	k2, _ := pyVenvKey("/usr/bin/python3", dir, false)
	if k1 != k2 {
		t.Error("unchanged project must reuse its venv key")
	}
	if len(m1) != 1 {
		t.Errorf("manifests = %v, want the requirements file", m1)
	}
	if err := os.WriteFile(req, []byte("requests==2.35.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if k3, _ := pyVenvKey("/usr/bin/python3", dir, false); k3 == k1 {
		t.Error("a dependency change must yield a new venv key")
	}
	// Coverage tooling is part of the environment's identity.
	if kc, _ := pyVenvKey("/usr/bin/python3", dir, true); kc == k1 {
		t.Error("coverage must partition the venv key")
	}
}
