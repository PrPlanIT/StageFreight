package test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// Python is the one dialect whose test command does NOT fetch its own dependencies:
// `go test` and `cargo test` resolve and build the dependency graph as part of running,
// but pytest is not even present in a bare CPython — it and the project's requirements
// must be installed first. So a Python suite gets an explicit, content-addressed
// environment step that the other dialects don't need.
//
// The venv lives in the SF cache namespace (rebuildable, ephemeral, and automatically
// bounded by the cache-root retention backstop), keyed by the CONTENT of the project's
// dependency manifests plus the interpreter path. A manifest edit yields a new key and
// a fresh install; an unchanged project reuses the warm venv across CI jobs exactly as
// GOMODCACHE/CARGO_HOME do.

// pyDepManifests are the dependency declarations a venv key is derived from, in the
// order pip would care about. Missing files are skipped.
var pyDepManifests = []string{
	"requirements.txt",
	"requirements-dev.txt",
	"requirements-test.txt",
	"pyproject.toml",
	"poetry.lock",
	"Pipfile.lock",
	"setup.cfg",
	"setup.py",
}

// pyTestDeps are the packages the runner itself requires, independent of the project:
// pytest for execution, and the JUnit XML transport is built in (--junitxml is core
// pytest, not a plugin). pytest-cov is added only when a suite requests coverage.
var pyTestDeps = []string{"pytest"}

// ensurePyVenv returns the path to a ready python interpreter inside a venv that has
// the project's dependencies plus pytest installed. Idempotent: a warm venv whose key
// matches is reused untouched.
func ensurePyVenv(ctx context.Context, pythonBin, projectDir string, coverage bool) (string, error) {
	key, manifests := pyVenvKey(pythonBin, projectDir, coverage)
	root := toolchain.ContainerCacheDir("python", "venv", key)
	venvPy := filepath.Join(root, "bin", "python")

	// Warm: the stamp is written last, so its presence proves the install completed.
	stamp := filepath.Join(root, ".sf-ready")
	if _, err := os.Stat(stamp); err == nil {
		if _, err := os.Stat(venvPy); err == nil {
			return venvPy, nil
		}
	}

	// Cold (or a half-built leftover): rebuild from scratch so a failed install can
	// never be mistaken for a usable environment.
	_ = os.RemoveAll(root)
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return "", fmt.Errorf("preparing venv cache: %w", err)
	}
	if out, err := runPy(ctx, projectDir, pythonBin, "-m", "venv", root); err != nil {
		return "", fmt.Errorf("creating venv: %w\n%s", err, out)
	}

	pipArgs := []string{"-m", "pip", "install", "--disable-pip-version-check", "--no-input"}
	install := append([]string{}, pipArgs...)
	install = append(install, pyTestDeps...)
	if coverage {
		install = append(install, "pytest-cov")
	}
	for _, m := range manifests {
		switch filepath.Base(m) {
		case "requirements.txt", "requirements-dev.txt", "requirements-test.txt":
			install = append(install, "-r", m)
		}
	}
	// A project declaring pyproject/setup.py is installed as a package so its own
	// modules import the way the test suite expects (pytest's rootdir alone does not
	// put the project on sys.path for a src-layout). A declared test EXTRA is the
	// standard place Python projects put their test-only dependencies, so it is
	// installed with the package rather than requiring a parallel requirements file.
	if hasAny(projectDir, "pyproject.toml", "setup.py", "setup.cfg") {
		target := "."
		if extra := testExtraName(projectDir); extra != "" {
			target = ".[" + extra + "]"
		}
		install = append(install, "-e", target)
	}
	if out, err := runPy(ctx, projectDir, venvPy, install...); err != nil {
		return "", fmt.Errorf("installing test dependencies: %w\n%s", err, out)
	}

	if err := os.WriteFile(stamp, []byte(key), 0o644); err != nil {
		return "", fmt.Errorf("stamping venv: %w", err)
	}
	return venvPy, nil
}

// pyVenvKey derives the content-addressed venv identity: the interpreter it is built
// from, whether coverage tooling is included, and the CONTENT of every dependency
// manifest present. Returns the key and the manifests that contributed (absolute).
func pyVenvKey(pythonBin, projectDir string, coverage bool) (string, []string) {
	h := sha256.New()
	fmt.Fprintf(h, "python=%s\ncov=%t\n", pythonBin, coverage)
	var found []string
	for _, name := range pyDepManifests {
		p := filepath.Join(projectDir, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s=%d\n", name, len(data))
		h.Write(data)
		found = append(found, p)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], found
}

// testExtraName returns the project's test extra ("test" or "dev", first match) as
// declared in pyproject.toml's [project.optional-dependencies], or "" when the project
// declares none. Read with the real TOML parser — a project's dependency declaration
// is not something to pattern-match at.
func testExtraName(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, "pyproject.toml"))
	if err != nil {
		return ""
	}
	var doc struct {
		Project struct {
			OptionalDependencies map[string][]string `toml:"optional-dependencies"`
		} `toml:"project"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	for _, name := range []string{"test", "tests", "dev"} {
		if _, ok := doc.Project.OptionalDependencies[name]; ok {
			return name
		}
	}
	return ""
}

func hasAny(dir string, names ...string) bool {
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}

// runPy executes a python/pip command in dir with a clean environment, returning the
// merged output for error context.
func runPy(ctx context.Context, dir, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	// A clean env with the interpreter on PATH: pip spawns child processes, and
	// PIP_* / PYTHONPATH inherited from a CI shell must not leak into the venv.
	env := toolchain.CleanEnv()
	env = setEnv(env, "PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	env = setEnv(env, "PIP_DISABLE_PIP_VERSION_CHECK", "1")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
