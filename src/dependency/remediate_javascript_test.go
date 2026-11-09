package dependency

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverJSProjects(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"package.json":                  `{"name": "app"}`,
		"yarn.lock":                     "# yarn\n\n__metadata:\n  version: 8\n",
		"package-lock.json":             `{}`, // loses to yarn.lock in the same directory
		"website/package.json":          `{"name": "site"}`,
		"website/yarn.lock":             "# yarn lockfile v1\n",
		"mono/package.json":             `{"name": "mono", "workspaces": ["pkgs/*"]}`,
		"mono/package-lock.json":        `{}`,
		"mono/pkgs/a/package.json":      `{"name": "a"}`,
		"mono/pkgs/a/package-lock.json": `{}`, // a workspace member: the root lock resolves it
		"pn/pnpm-workspace.yaml":        "packages: ['*']\n",
		"pn/package.json":               `{}`,
		"pn/pnpm-lock.yaml":             "lockfileVersion: '9.0'\n",
		"pn/b/package.json":             `{}`,
		"pn/b/pnpm-lock.yaml":           "lockfileVersion: '9.0'\n",
		"loose/package-lock.json":       `{}`, // its package.json is untracked
		"docs/package.json":             `{}`,
		"docs/package-lock.json":        `{}`, // lint.exclude kept it out of the scan
	}
	tracked := map[string]bool{}
	resolved := map[string]bool{}
	for rel, body := range files {
		if path.Base(rel) == "package.json" && rel != "docs/package.json" {
			resolved[rel] = true
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		tracked[rel] = true
	}
	if err := os.WriteFile(filepath.Join(root, "loose", "package.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved["loose/package.json"] = true // resolved but untracked: still not a project

	var got []string
	for _, p := range discoverJSProjects(root, tracked, resolved) {
		got = append(got, p.lockfile+" "+p.manager)
	}
	want := []string{
		"mono/package-lock.json npm",
		"pn/pnpm-lock.yaml pnpm",
		"website/yarn.lock yarn-classic",
		"yarn.lock yarn",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("discoverJSProjects = %q, want %q", got, want)
	}
}

func TestOverrideRelation(t *testing.T) {
	cases := []struct {
		locked, fix string
		want        versionRelation
	}{
		{"1.2.0", "1.4.1", relationCompatible},
		{"1.4.1", "1.4.1", relationCompatible},
		{"0.4.1", "0.4.2", relationCompatible},
		{"0.4.1", "0.5.1", relationIncompatible}, // 0.x: the minor is the line
		{"1.9.0", "2.0.1", relationIncompatible},
		{"3.0.0", "2.0.1", relationAbove},
		{"0.6.0", "0.5.1", relationAbove},
		{"1.0.0", "0.5.1", relationAbove},
		{"not-a-version", "1.0.0", relationUnparsed},
	}
	for _, c := range cases {
		if got := overrideRelation(c.locked, c.fix); got != c.want {
			t.Errorf("overrideRelation(%s, %s) = %d, want %d", c.locked, c.fix, got, c.want)
		}
	}
}

func TestOverrideHeld(t *testing.T) {
	npm := jsProject{dir: t.TempDir(), manager: jsManagerNpm}
	manifest := []byte(`{"name": "p", "overrides": {"mine": "1.0.0"}}`)
	held := func(mode string, p jsProject, name, fix string, copies ...string) string {
		r := &jsRemediator{cfg: UpdateConfig{TransitiveOverride: mode}}
		return r.overrideHeld(p, manifest, name, fix, copies)
	}

	if got := held("", npm, "cookie", "0.7.2", "0.7.1"); got != "" {
		t.Errorf("compatible copy held by default mode: %s", got)
	}
	if got := held("off", npm, "cookie", "0.7.2", "0.7.1"); !strings.Contains(got, "off") {
		t.Errorf("off mode: %q", got)
	}
	if got := held("compatible", npm, "decode-uri-component", "0.5.1", "0.4.1"); !strings.Contains(got, "not semver-compatible") {
		t.Errorf("compatible mode, older line: %q", got)
	}
	if got := held("any", npm, "decode-uri-component", "0.5.1", "0.4.1"); got != "" {
		t.Errorf("any mode, older line held: %s", got)
	}
	if got := held("any", npm, "x", "1.2.3", "1.0.0", "2.0.0"); !strings.Contains(got, "newer line") {
		t.Errorf("any mode, copy above the fix: %q", got)
	}
	if got := held("any", npm, "mine", "1.0.1", "1.0.0"); !strings.Contains(got, "did not write") {
		t.Errorf("operator-written override: %q", got)
	}

	pnpm := jsProject{dir: t.TempDir(), manager: jsManagerPnpm}
	if err := os.WriteFile(filepath.Join(pnpm.dir, "pnpm-workspace.yaml"), []byte("overrides:\n  a: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := held("any", pnpm, "cookie", "0.7.2", "0.7.1"); !strings.Contains(got, "pnpm-workspace.yaml") {
		t.Errorf("pnpm workspace overrides: %q", got)
	}
}

func TestOverridesLockRoundTrip(t *testing.T) {
	root := t.TempDir()
	l, err := readOverridesLock(root)
	if err != nil || len(l.Overrides) != 0 {
		t.Fatalf("missing lock: %v %v", l, err)
	}
	l.set(overrideFloor{Manifest: "web/package.json", Package: "b", Floor: "^1.0.1", Advisories: []string{"GHSA-1"}})
	l.set(overrideFloor{Manifest: "package.json", Package: "a", Floor: "^0.5.1"})
	l.set(overrideFloor{Manifest: "web/package.json", Package: "b", Floor: "^1.0.2"}) // replaces
	if err := l.write(root); err != nil {
		t.Fatal(err)
	}

	back, err := readOverridesLock(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []overrideFloor{
		{Manifest: "package.json", Package: "a", Floor: "^0.5.1"},
		{Manifest: "web/package.json", Package: "b", Floor: "^1.0.2"},
	}
	if !reflect.DeepEqual(back.Overrides, want) || back.LockfileVersion != overridesLockVersion {
		t.Fatalf("round trip = %+v", back)
	}
	if got := back.forManifest("web/package.json"); len(got) != 1 || got[0].Package != "b" {
		t.Fatalf("forManifest = %+v", got)
	}

	back.drop("package.json", "a")
	back.drop("web/package.json", "b")
	if err := back.write(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(overridesLockPath(root)); !os.IsNotExist(err) {
		t.Fatalf("empty lock not removed: %v", err)
	}
}

func TestJSRequirers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	npmLock := write("package-lock.json", `{
  "lockfileVersion": 3,
  "packages": {
    "": {"dependencies": {"express": "^4.21.0", "cookie": "^0.7.0"}},
    "node_modules/express": {"version": "4.21.2", "dependencies": {"cookie": "0.7.1"}},
    "node_modules/a/node_modules/@s/b": {"version": "2.0.0", "optionalDependencies": {"cookie": "~0.7.0"}},
    "node_modules/cookie": {"version": "0.7.1"}
  }
}`)
	if got, want := jsRequirers(npmLock, jsManagerNpm, "cookie"), []string{
		"@s/b@2.0.0 wants ~0.7.0",
		"express@4.21.2 wants 0.7.1",
		"the project wants ^0.7.0",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("npm = %q, want %q", got, want)
	}

	yarnLock := write("yarn.lock", `__metadata:
  version: 8

"query-string@npm:^9.0.0":
  version: 9.3.1
  dependencies:
    decode-uri-component: "npm:^0.4.1"

"@s/x@npm:^1.0.0, @s/x@npm:^1.1.0":
  version: 1.2.0
  dependencies:
    decode-uri-component: "npm:^0.4.1"

"web@workspace:.":
  version: 0.0.0-use.local
  dependencies:
    decode-uri-component: "npm:^0.4.0"
`)
	if got, want := jsRequirers(yarnLock, jsManagerYarn, "decode-uri-component"), []string{
		"@s/x@1.2.0 wants ^0.4.1",
		"query-string@9.3.1 wants ^0.4.1",
		"the project wants ^0.4.0",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("yarn = %q, want %q", got, want)
	}

	pnpmLock := write("pnpm-lock.yaml", `lockfileVersion: '9.0'
snapshots:
  express@4.21.2:
    dependencies:
      cookie: 0.7.1
  vite@5.0.0(@types/node@20.0.0):
    dependencies:
      cookie: 0.7.1(debug@4.0.0)
`)
	if got, want := jsRequirers(pnpmLock, jsManagerPnpm, "cookie"), []string{
		"express@4.21.2 wants 0.7.1",
		"vite@5.0.0 wants 0.7.1",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("pnpm = %q, want %q", got, want)
	}

	if got := jsRequirers(filepath.Join(dir, "missing"), jsManagerNpm, "cookie"); got != nil {
		t.Errorf("missing lockfile = %q", got)
	}
}
