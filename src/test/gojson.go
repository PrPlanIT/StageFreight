package test

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// goTestEvent is one line of `go test -json` (the test2json schema). go test's JSON
// is TRANSPORT data; we parse it into StageFreight-native PackageResults.
type goTestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// parseGoTest consumes a `go test -json` stream into per-package results (in first-
// seen order). modulePath trims import paths to module-relative form; synopses maps
// import path → doc one-liner. onDone (optional) fires as each package finishes —
// for live progress — receiving a lightweight result (failures not yet attached).
func parseGoTest(r io.Reader, modulePath string, synopses map[string]string, onDone func(PackageResult)) []PackageResult {
	type acc struct {
		pr     PackageResult
		failed []string                    // failed test names (any depth), in order
		out    map[string]*strings.Builder // per-test output buffers
		pkgOut *strings.Builder            // package-level output (build/compile errors)
		seen   map[string]bool
	}
	accs := map[string]*acc{}
	order := []string{}
	get := func(p string) *acc {
		a := accs[p]
		if a == nil {
			a = &acc{
				pr:   PackageResult{ImportPath: p, Rel: relImport(modulePath, p), Synopsis: synopses[p], Coverage: -1},
				out:  map[string]*strings.Builder{},
				seen: map[string]bool{},
			}
			accs[p] = a
			order = append(order, p)
		}
		return a
	}
	// finalize attaches a package's leaf failures (with output) once its terminal
	// event arrives, so onDone streams a COMPLETE result (failures expandable live).
	finalize := func(a *acc) {
		for _, name := range a.failed {
			if !isLeafFailure(name, a.failed) {
				continue // keep the actual failing assertion, not its parent
			}
			var o string
			if b := a.out[name]; b != nil {
				o = b.String()
			}
			a.pr.Failures = append(a.pr.Failures, TestFailure{Name: name, Output: o})
		}
		// A package that failed without a failing test = a build/compile failure;
		// surface its package-level output so the structured view still says why.
		if a.pr.Status == StatusFailed && len(a.pr.Failures) == 0 && a.pkgOut != nil {
			a.pr.Failures = append(a.pr.Failures, TestFailure{Name: "(build/run)", Output: a.pkgOut.String()})
		}
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e goTestEvent
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Package == "" {
			continue
		}
		a := get(e.Package)

		if e.Test == "" { // package-level event
			switch e.Action {
			case "output": // build errors + the "coverage: X%" line arrive here
				if c, ok := parseGoCoverage(e.Output); ok {
					a.pr.Coverage = c
				}
				if a.pkgOut == nil {
					a.pkgOut = &strings.Builder{}
				}
				a.pkgOut.WriteString(e.Output)
			case "pass":
				a.pr.Status, a.pr.Duration = StatusPassed, secs(e.Elapsed)
				finalize(a)
				notifyDone(onDone, a.pr)
			case "fail":
				a.pr.Status, a.pr.Duration = StatusFailed, secs(e.Elapsed)
				finalize(a)
				notifyDone(onDone, a.pr)
			case "skip":
				a.pr.Status, a.pr.Duration = StatusSkipped, secs(e.Elapsed) // "no test files"
				notifyDone(onDone, a.pr)
			}
			continue
		}

		// test-level event
		top := !strings.Contains(e.Test, "/")
		switch e.Action {
		case "pass":
			if top {
				a.pr.Tests++
			}
		case "fail":
			if top {
				a.pr.Tests++
			}
			if !a.seen[e.Test] {
				a.seen[e.Test] = true
				a.failed = append(a.failed, e.Test)
			}
		case "output":
			b := a.out[e.Test]
			if b == nil {
				b = &strings.Builder{}
				a.out[e.Test] = b
			}
			b.WriteString(e.Output)
		}
	}

	out := make([]PackageResult, 0, len(order))
	for _, p := range order {
		out = append(out, accs[p].pr) // already finalized at each terminal event
	}
	return out
}

func notifyDone(onDone func(PackageResult), p PackageResult) {
	if onDone != nil {
		onDone(p)
	}
}

// isLeafFailure reports whether name has no failing child (so we show the deepest
// failing test, e.g. TestResolveTransport/https_remote rather than the parent).
func isLeafFailure(name string, all []string) bool {
	for _, o := range all {
		if o != name && strings.HasPrefix(o, name+"/") {
			return false
		}
	}
	return true
}

func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// parseGoCoverage extracts the percentage from go test's "coverage: 73.2% of
// statements" line (emitted per package under -cover). false when absent.
func parseGoCoverage(s string) (float64, bool) {
	const marker = "coverage: "
	i := strings.Index(s, marker)
	if i < 0 {
		return 0, false
	}
	rest := s[i+len(marker):]
	j := strings.IndexByte(rest, '%')
	if j < 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(rest[:j]), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// relImport trims a full import path to module-relative form (src/commit), keeping
// the location without the long module prefix.
func relImport(mod, imp string) string {
	switch {
	case mod == "":
		return imp
	case imp == mod:
		return "."
	case strings.HasPrefix(imp, mod+"/"):
		return imp[len(mod)+1:]
	default:
		return imp
	}
}

// goModulePath returns the main module path (`go list -m`), best-effort.
func goModulePath(goBin, dir string, env []string) string {
	cmd := exec.Command(goBin, "list", "-m")
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// goSynopses maps each package's import path to its doc-comment one-liner
// (`go list -f '{{.ImportPath}}\t{{.Doc}}'`), best-effort. This is what lets a row
// say what an area IS in plain language instead of just a Go import path.
func goSynopses(goBin, dir string, env []string) map[string]string {
	m := map[string]string{}
	// -e: tolerate a single unloadable package instead of exiting non-zero (which
	// would make Output() discard ALL the synopses). Parse stdout best-effort —
	// Output() returns captured stdout even on a non-zero exit.
	cmd := exec.Command(goBin, "list", "-e", "-f", "{{.ImportPath}}\t{{.Doc}}", "./...")
	cmd.Dir, cmd.Env = dir, env
	out, _ := cmd.Output()
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.IndexByte(line, '\t'); i > 0 {
			if doc := cleanSynopsis(line[i+1:]); doc != "" {
				m[line[:i]] = doc
			}
		}
	}
	return m
}

// cleanSynopsis turns a go doc synopsis into a terse row label: drops the
// conventional "Package <name> " prefix (redundant — the row already shows the
// path) and caps length so a row reads on one line. "" when there is no doc.
func cleanSynopsis(doc string) string {
	doc = strings.TrimSpace(doc)
	if rest, ok := strings.CutPrefix(doc, "Package "); ok {
		if sp := strings.IndexByte(rest, ' '); sp > 0 {
			doc = strings.TrimSpace(rest[sp+1:]) // drop the package-name word
		} else {
			doc = ""
		}
	}
	const max = 46
	if r := []rune(doc); len(r) > max {
		doc = strings.TrimRight(string(r[:max-1]), " ,;:—-") + "…"
	}
	return doc
}

// injectAfter returns argv with v inserted right after index i.
func injectAfter(argv []string, i int, v string) []string {
	out := make([]string, 0, len(argv)+1)
	out = append(out, argv[:i+1]...)
	out = append(out, v)
	out = append(out, argv[i+1:]...)
	return out
}

// firstErrLine returns the first meaningful line of a failing test's output (the
// assertion/error), skipping go's === / --- framing and blank lines.
func firstErrLine(output string) string {
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "===") || strings.HasPrefix(line, "---") {
			continue
		}
		if len(line) > 120 {
			line = line[:117] + "..."
		}
		return line
	}
	return ""
}
