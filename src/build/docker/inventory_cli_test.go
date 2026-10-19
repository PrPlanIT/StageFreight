package docker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// managerFixtures gives each package manager a program spelling, an install subcommand
// and an operand it must inventory. Every manager in packageManagers must have one: the
// grammar tests below run against all of them, so a manager cannot ship untested.
var managerFixtures = map[string]struct {
	program string
	install string
	pkg     string
}{
	"apk":    {"apk", "add", "curl"},
	"apt":    {"apt-get", "install", "curl"},
	"pip":    {"pip3", "install", "requests"},
	"galaxy": {"ansible-galaxy", "collection install", "community.general"},
	"npm":    {"npm", "install", "typescript"},
	"go":     {"go", "install", "golang.org/x/tools/cmd/goimports@v0.30.0"},
}

func TestPackageManagers_AllHaveFixtures(t *testing.T) {
	for _, m := range packageManagers {
		if _, ok := managerFixtures[m.name]; !ok {
			t.Errorf("package manager %q has no fixture in managerFixtures", m.name)
		}
	}
}

func extractNames(t *testing.T, m packageManager, cmd string) []string {
	t.Helper()
	pkgs, ok := m.extract(cmd, nil)
	if !ok {
		t.Fatalf("%s: %q not read as an install command", m.name, cmd)
	}
	var names []string
	for _, p := range pkgs {
		names = append(names, p.Name)
	}
	return names
}

// An option's value is never an operand, in any spelling the grammar declares, with the
// value as the next word or attached with "=". Regression: `ansible-galaxy collection
// install --collections-path /usr/share/ansible/collections …` inventoried the path as a
// collection.
func TestPackageManagers_OptionValuesAreNotPackages(t *testing.T) {
	for _, m := range packageManagers {
		fx := managerFixtures[m.name]
		want, _, _, _ := m.operand(fx.pkg)
		for spelling, opt := range m.cli.options {
			value := "/opt/value"
			if opt.words != nil {
				value = "ERRSIG BADSIG"
			}
			spellings := []string{spelling}
			if m.cli.singleDash {
				spellings = append(spellings, strings.TrimPrefix(spelling, "-"))
			}
			for _, s := range spellings {
				for _, cmd := range []string{
					fx.program + " " + fx.install + " " + s + " " + value + " " + fx.pkg,
					fx.program + " " + s + "=" + strings.Fields(value)[0] + " " + fx.install + " " + fx.pkg,
				} {
					if got := extractNames(t, m, cmd); !slices.Equal(got, []string{want}) {
						t.Errorf("%s: %q inventoried %v, want [%s]", m.name, cmd, got, want)
					}
				}
			}
		}
	}
}

// Options before the subcommand do not hide it, and other subcommands install nothing.
func TestPackageManagers_Subcommand(t *testing.T) {
	for _, m := range packageManagers {
		fx := managerFixtures[m.name]
		if _, ok := m.extract(fx.program+" "+fx.install+" "+fx.pkg, nil); !ok {
			t.Errorf("%s: install not recognised", m.name)
		}
		if _, ok := m.extract(fx.program+" list "+fx.pkg, nil); ok {
			t.Errorf("%s: a non-install subcommand was read as an install", m.name)
		}
		if _, ok := m.extract("echo "+fx.program+" "+fx.install+" "+fx.pkg, nil); ok {
			t.Errorf("%s: the program as another command's argument was read as an install", m.name)
		}
	}
}

func TestPackageOperands(t *testing.T) {
	type want struct {
		name, version string
		pinned, ok    bool
	}
	cases := []struct {
		manager string
		word    string
		want    want
	}{
		{"apk", "curl", want{"curl", "", false, true}},
		{"apk", "curl=8.11.1-r0", want{"curl", "8.11.1-r0", true, true}},
		{"apk", "curl~8.11", want{"curl", "8.11", false, true}},
		{"apk", "curl>=8", want{"curl", "8", false, true}},
		{"apk", "py3-pip@edge", want{"py3-pip", "", false, true}},
		{"apk", ".build-deps", want{}},
		{"apk", "/tmp/pkg.apk", want{}},

		{"apt", "curl", want{"curl", "", false, true}},
		{"apt", "curl=7.88.1-10", want{"curl", "7.88.1-10", true, true}},
		{"apt", "libc6:i386", want{"libc6", "", false, true}},
		{"apt", "nginx/bookworm-backports", want{"nginx", "", false, true}},
		{"apt", "g++", want{"g++", "", false, true}},
		{"apt", "./local.deb", want{}},
		{"apt", "exim4-", want{}},

		{"pip", "requests", want{"requests", "", false, true}},
		{"pip", "requests==2.32.3", want{"requests", "2.32.3", true, true}},
		{"pip", "requests===2.32.3", want{"requests", "2.32.3", true, true}},
		{"pip", "requests==2.*", want{"requests", "2.*", false, true}},
		{"pip", "requests>=2.31,<3", want{"requests", "2.31", false, true}},
		{"pip", "requests~=2.31", want{"requests", "2.31", false, true}},
		{"pip", "requests<3", want{"requests", "", false, true}},
		{"pip", "uvicorn[standard]==0.30.0", want{"uvicorn", "0.30.0", true, true}},
		{"pip", "pkg @ https://example.com/pkg.zip", want{"pkg", "", false, true}},
		{"pip", "./src", want{}},
		{"pip", ".", want{}},
		{"pip", "dist/pkg-1.0-py3-none-any.whl", want{}},
		{"pip", "pkg-1.0.tar.gz", want{}},
		{"pip", "git+https://github.com/o/r", want{}},

		{"galaxy", "community.general", want{"community.general", "", false, true}},
		{"galaxy", "community.general:10.1.0", want{"community.general", "10.1.0", true, true}},
		{"galaxy", "community.general:==10.1.0", want{"community.general", "10.1.0", true, true}},
		{"galaxy", "community.general:>=10.0.0", want{"community.general", "10.0.0", false, true}},
		{"galaxy", "community.general:>=10.0.0,<11.0.0", want{"community.general", "10.0.0", false, true}},
		{"galaxy", "community.general:*", want{"community.general", "", false, true}},
		{"galaxy", "geerlingguy.docker,7.4.1", want{"geerlingguy.docker", "7.4.1", true, true}},
		{"galaxy", "/usr/share/ansible/collections", want{}},
		{"galaxy", "community-general-10.1.0.tar.gz", want{}},
		{"galaxy", "https://github.com/o/r.git", want{}},
		{"galaxy", "git+https://github.com/o/r.git,main", want{}},

		{"npm", "typescript", want{"typescript", "", false, true}},
		{"npm", "typescript@5.7.2", want{"typescript", "5.7.2", true, true}},
		{"npm", "typescript@^5.7.0", want{"typescript", "^5.7.0", false, true}},
		{"npm", "npm@latest", want{"npm", "latest", false, true}},
		{"npm", "@angular/cli@19.0.0", want{"@angular/cli", "19.0.0", true, true}},
		{"npm", "@angular/cli", want{"@angular/cli", "", false, true}},
		{"npm", "./local-pkg", want{}},
		{"npm", "pkg-1.0.0.tgz", want{}},
		{"npm", "user/repo", want{}},
		{"npm", "git+https://github.com/o/r.git", want{}},

		{"go", "golang.org/x/tools/cmd/goimports@v0.30.0", want{"golang.org/x/tools/cmd/goimports", "v0.30.0", true, true}},
		{"go", "github.com/o/r/cmd/x@latest", want{"github.com/o/r/cmd/x", "latest", false, true}},
		{"go", "github.com/o/r/cmd/x@v0.0.0-20240101000000-abcdef123456", want{"github.com/o/r/cmd/x", "v0.0.0-20240101000000-abcdef123456", true, true}},
		{"go", "github.com/o/r/cmd/x@abcdef1", want{"github.com/o/r/cmd/x", "abcdef1", true, true}},
		{"go", "github.com/o/r/cmd/x@main", want{"github.com/o/r/cmd/x", "main", false, true}},
		{"go", "./cmd/x", want{}},
		{"go", "github.com/o/r/...", want{}},
		{"go", "std", want{}},
	}
	byName := map[string]packageManager{}
	for _, m := range packageManagers {
		byName[m.name] = m
	}
	for _, c := range cases {
		name, version, pinned, ok := byName[c.manager].operand(c.word)
		got := want{name, version, pinned, ok}
		if !ok {
			got = want{}
		}
		if got != c.want {
			t.Errorf("%s %q: got %+v, want %+v", c.manager, c.word, got, c.want)
		}
	}
}

func TestShellWords(t *testing.T) {
	cases := map[string][]string{
		`go install -ldflags '-s -w' x.io/y@v1.0.0`: {"go", "install", "-ldflags", "-s -w", "x.io/y@v1.0.0"},
		`pip install "requests>=2" 'a b'`:           {"pip", "install", "requests>=2", "a b"},
		`echo "a \"b\" \$c" d\ e`:                   {"echo", `a "b" $c`, "d e"},
	}
	for in, want := range cases {
		if got := shellWords(in); !slices.Equal(got, want) {
			t.Errorf("shellWords(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitShellCommands(t *testing.T) {
	got := splitShellCommands(`a && b || c; d | e & f 2>&1 &>/dev/null; echo "x && y; z"`)
	var trimmed []string
	for _, c := range got {
		if c = strings.TrimSpace(c); c != "" {
			trimmed = append(trimmed, c)
		}
	}
	want := []string{"a", "b", "c", "d", "e", "f 2>&1 &>/dev/null", `echo "x && y; z"`}
	if !slices.Equal(trimmed, want) {
		t.Errorf("got %q, want %q", trimmed, want)
	}
}

// The program is the command word: RUN flags, variable assignments, wrappers and
// `python -m` lead to it; redirections are not operands.
func TestExtractRunPackages_CommandForms(t *testing.T) {
	cases := map[string][]string{
		`--mount=type=cache,target=/root/.cache --network=host pip install requests`:     {"requests"},
		`DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends curl`: {"curl"},
		`sudo -E apt-get install -y curl`:                                                {"curl"},
		`python3 -m pip install --no-cache-dir requests`:                                 {"requests"},
		`pip install requests > /dev/null 2>&1`:                                          {"requests"},
		`go install -ldflags '-s -w' -tags netgo x.io/y@v1.2.3`:                          {"x.io/y"},
		`npm install -g --prefix /usr/local typescript@5.7.2`:                            {"typescript"},
		`apk --no-cache -X https://dl-cdn.alpinelinux.org/alpine/edge/testing add curl`:  {"curl"},
	}
	for body, want := range cases {
		pkgs, _ := extractRunPackages(body, nil)
		var got []string
		for _, p := range pkgs {
			got = append(got, p.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("RUN %s: got %v, want %v", body, got, want)
		}
	}
}

// The hlhd/ansible Dockerfile's collection install: the --collections-path value is not
// a collection.
func TestExtractInventory_GalaxyCollectionsPathNotACollection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	const body = `FROM python:3.14.8-alpine3.23
RUN ansible-galaxy collection install --collections-path /usr/share/ansible/collections \
    ansible.posix \
    community.general \
    kubernetes.core
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ExtractInventory(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range res.Packages {
		if p.Manager == "galaxy" {
			got = append(got, p.Name)
		}
	}
	if want := []string{"ansible.posix", "community.general", "kubernetes.core"}; !slices.Equal(got, want) {
		t.Errorf("galaxy inventory = %v, want %v", got, want)
	}
}
