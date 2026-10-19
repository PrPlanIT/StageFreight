package docker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// testdata/cli holds each tool's own option table, captured from the version named on its
// first line:
//
//	apk.txt     docker run alpine:3.23 sh -c 'apk --version; apk add --help; apk del --help'
//	pip.txt     docker run python:3.14-alpine sh -c 'pip --version; pip install --help'
//	galaxy.txt  ansible-galaxy --version | head -1; ansible-galaxy {collection,role} install --help
//	npm.txt     npm --version; the Definition/type/short lines of @npmcli/config's definitions.js
//	go.txt      docker run golang sh -c 'go version; go help build'
//	apt.cc      apt's install option blocks from apt-private/private-cmndline.cc (see its header)
//
// Refresh a fixture when the tool's supported version moves; the grammar must then match it.

// valueSpellings maps every spelling of an option that takes a value to whether it takes
// one word (false) or every following word of a set (true).
type valueSpellings map[string]bool

func readFixture(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "cli", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(b), "\n")
}

var (
	// "  --root, -p ROOT   Manage…" — a placeholder one space after the spellings.
	apkOptionRe = regexp.MustCompile(`^  (--[a-z0-9-]+(?:, -[A-Za-z])*) [A-Z][A-Z_]+(?:\s|$)`)
	// "  -r, --requirement <file>"
	pipOptionRe = regexp.MustCompile(`^  ((?:-[A-Za-z], )?--[a-z0-9-]+) <`)
	// "  -p, --collections-path COLLECTIONS_PATH", "  --x {A,B} [{A,B} ...]"
	galaxyOptionRe = regexp.MustCompile(`^  ((?:-[A-Za-z]|--[a-z0-9-]+)(?:, (?:-[A-Za-z]|--[a-z0-9-]+))*) ([A-Z_]+|\{[^}]+\})(.*)$`)
	// "\t-ldflags '[pattern=]arg list'", "\t-p n"
	goOptionRe = regexp.MustCompile(`^\t(-[A-Za-z]+) \S`)
	// addArg('t', "target-release", "APT::Default-Release", CommandLine::HasArg);
	aptOptionRe = regexp.MustCompile(`addArg\((?:'(.)'|0), ?"([a-z-]+)", ?[^,]+, ?CommandLine::(HasArg|ConfigFile|ArbItem)\)`)

	npmDefinitionRe = regexp.MustCompile(`^  \S+: new Definition\('([^']+)'`)
	npmShortRe      = regexp.MustCompile(`^    short: '(.)'`)
)

func spellingsOf(list string) []string {
	return strings.Split(list, ", ")
}

func apkValueOptions(t *testing.T) valueSpellings {
	out := valueSpellings{}
	for _, line := range readFixture(t, "apk.txt") {
		if m := apkOptionRe.FindStringSubmatch(line); m != nil {
			for _, s := range spellingsOf(m[1]) {
				out[s] = false
			}
		}
	}
	return out
}

func pipValueOptions(t *testing.T) valueSpellings {
	out := valueSpellings{}
	for _, line := range readFixture(t, "pip.txt") {
		if m := pipOptionRe.FindStringSubmatch(line); m != nil {
			for _, s := range spellingsOf(m[1]) {
				out[s] = false
			}
		}
	}
	return out
}

func galaxyValueOptions(t *testing.T) valueSpellings {
	out := valueSpellings{}
	for _, line := range readFixture(t, "galaxy.txt") {
		if m := galaxyOptionRe.FindStringSubmatch(line); m != nil {
			for _, s := range spellingsOf(m[1]) {
				out[s] = strings.Contains(m[3], "...]") // argparse nargs="+"
			}
		}
	}
	return out
}

func goValueOptions(t *testing.T) valueSpellings {
	out := valueSpellings{}
	for _, line := range readFixture(t, "go.txt") {
		if m := goOptionRe.FindStringSubmatch(line); m != nil {
			out["-"+m[1]] = false // the grammar keys go's flags by their double-dash spelling
		}
	}
	return out
}

func aptValueOptions(t *testing.T) valueSpellings {
	out := valueSpellings{}
	for _, line := range readFixture(t, "apt.cc") {
		for _, m := range aptOptionRe.FindAllStringSubmatch(line, -1) {
			out["--"+m[2]] = false
			if m[1] != "" {
				out["-"+m[1]] = false
			}
		}
	}
	return out
}

// npm's parser gives an option a value unless its type admits Boolean.
func npmValueOptions(t *testing.T) valueSpellings {
	out := valueSpellings{}
	var name, short string
	boolean, inType := false, false
	flush := func() {
		if name != "" && !boolean {
			out["--"+name] = false
			if short != "" {
				out["-"+short] = false
			}
		}
	}
	for _, line := range readFixture(t, "npm.txt") {
		switch {
		case npmDefinitionRe.MatchString(line):
			flush()
			name = npmDefinitionRe.FindStringSubmatch(line)[1]
			short, boolean, inType = "", false, false
		case strings.HasPrefix(line, "    type: "):
			boolean = strings.Contains(line, "Boolean")
			inType = strings.Contains(line, "[") && !strings.Contains(line, "]")
		case inType:
			boolean = boolean || strings.Contains(line, "Boolean")
			inType = !strings.Contains(line, "]")
		case npmShortRe.MatchString(line):
			short = npmShortRe.FindStringSubmatch(line)[1]
		}
	}
	flush()
	return out
}

// Every grammar declares exactly the options its tool's option table gives a value: one
// it misses reads a value as a package, and one it invents swallows a package.
func TestGrammars_MatchToolOptionTables(t *testing.T) {
	tables := map[string]func(*testing.T) valueSpellings{
		"apk": apkValueOptions, "apt": aptValueOptions, "pip": pipValueOptions,
		"galaxy": galaxyValueOptions, "npm": npmValueOptions, "go": goValueOptions,
	}
	for _, m := range packageManagers {
		table, ok := tables[m.name]
		if !ok {
			t.Errorf("package manager %q has no option table in testdata/cli", m.name)
			continue
		}
		want := table(t)
		if len(want) == 0 {
			t.Errorf("%s: no value-taking options parsed from its option table", m.name)
			continue
		}
		var missing, invented, arity []string
		for s, greedy := range want {
			opt, ok := m.cli.options[s]
			switch {
			case !ok:
				missing = append(missing, s)
			case (opt.words != nil) != greedy:
				arity = append(arity, s)
			}
		}
		for s := range m.cli.options {
			if _, ok := want[s]; !ok {
				invented = append(invented, s)
			}
		}
		sort.Strings(missing)
		sort.Strings(invented)
		sort.Strings(arity)
		if len(missing) > 0 {
			t.Errorf("%s grammar is missing value-taking options: %v", m.name, missing)
		}
		if len(invented) > 0 {
			t.Errorf("%s grammar declares options its tool does not give a value: %v", m.name, invented)
		}
		if len(arity) > 0 {
			t.Errorf("%s grammar has the wrong arity for: %v", m.name, arity)
		}
	}
}
