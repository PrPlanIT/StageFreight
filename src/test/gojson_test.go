package test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseGoCoverage(t *testing.T) {
	cases := map[string]float64{
		"coverage: 73.2% of statements\n": 73.2,
		"coverage: 100.0% of statements":  100.0,
		"coverage: 0.0% of statements":    0.0,
	}
	for in, want := range cases {
		if got, ok := parseGoCoverage(in); !ok || got != want {
			t.Errorf("parseGoCoverage(%q) = %v,%v; want %v,true", in, got, ok, want)
		}
	}
	if _, ok := parseGoCoverage("PASS\n"); ok {
		t.Error("a line without coverage should return ok=false")
	}
}

// go test -json emits the coverage as a package-level output event under -cover.
func TestParseGoTest_Coverage(t *testing.T) {
	const stream = `{"Action":"pass","Package":"x/foo","Test":"TestA","Elapsed":0}
{"Action":"output","Package":"x/foo","Output":"coverage: 73.2% of statements\n"}
{"Action":"pass","Package":"x/foo","Elapsed":0.01}
`
	pkgs, _ := parseGoTest(strings.NewReader(stream), "x", nil, nil)
	if len(pkgs) != 1 {
		t.Fatalf("want 1 package, got %d", len(pkgs))
	}
	if pkgs[0].Coverage != 73.2 {
		t.Errorf("coverage = %v, want 73.2", pkgs[0].Coverage)
	}
	if pkgs[0].Status != StatusPassed || pkgs[0].Tests != 1 {
		t.Errorf("pkg = %+v", pkgs[0])
	}
}

func TestParseCoverTotal(t *testing.T) {
	const funcOut = `github.com/x/foo/a.go:10:	Add		100.0%
github.com/x/foo/b.go:20:	Sub		0.0%
total:				(statements)	73.2%
`
	if got, ok := parseCoverTotal(funcOut); !ok || got != 73.2 {
		t.Errorf("parseCoverTotal = %v,%v; want 73.2,true", got, ok)
	}
	if _, ok := parseCoverTotal("no total line here\n"); ok {
		t.Error("missing total line should return ok=false")
	}
}

// Without -cover there is no coverage line, so Coverage stays "not measured" (<0).
func TestParseGoTest_NoCoverage(t *testing.T) {
	const stream = `{"Action":"pass","Package":"x/foo","Test":"TestA","Elapsed":0}
{"Action":"pass","Package":"x/foo","Elapsed":0.01}
`
	pkgs, _ := parseGoTest(strings.NewReader(stream), "x", nil, nil)
	if len(pkgs) != 1 || pkgs[0].Coverage >= 0 {
		t.Errorf("want 1 pkg with Coverage<0, got %+v", pkgs)
	}
}

// TestParseGoTest_CapturesPackagelessError ensures a top-level go test error that
// carries no Package (e.g. `exec: "go" not found`) is returned as the second value,
// so a command-level failure isn't reasonless. Regression for the opaque-suite bug.
func TestParseGoTest_CapturesPackagelessError(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"output","Output":"exec: \"go\": executable file not found in $PATH\n"}`,
		`{"Action":"output","Package":"x/foo","Output":"ok\n"}`,
		`{"Action":"pass","Package":"x/foo","Elapsed":0.1}`,
	}, "\n")
	pkgs, top := parseGoTest(strings.NewReader(stream), "x", nil, nil)
	if len(pkgs) != 1 {
		t.Fatalf("packages = %d, want 1", len(pkgs))
	}
	if !strings.Contains(top, "executable file not found") {
		t.Errorf("second return must capture the package-less error; got %q", top)
	}
}

// A compile error (build-output events) reaches the failing package's failure output.
func TestParseGoTest_SurfacesCompileErrors(t *testing.T) {
	const stream = `{"ImportPath":"x/foo [x/foo.test]","Action":"build-output","Output":"# x/foo [x/foo.test]\n"}
{"ImportPath":"x/foo [x/foo.test]","Action":"build-output","Output":"foo/bar.go:12:2: undefined: Missing\n"}
{"ImportPath":"x/foo [x/foo.test]","Action":"build-fail"}
{"Action":"start","Package":"x/foo"}
{"Action":"output","Package":"x/foo","Output":"FAIL\tx/foo [build failed]\n"}
{"Action":"fail","Package":"x/foo","Elapsed":0,"FailedBuild":"x/foo [x/foo.test]"}
`
	pkgs, _ := parseGoTest(strings.NewReader(stream), "x", nil, nil)
	if len(pkgs) != 1 || pkgs[0].Status != StatusFailed {
		t.Fatalf("pkgs = %+v", pkgs)
	}
	if len(pkgs[0].Failures) != 1 || !strings.Contains(pkgs[0].Failures[0].Output, "foo/bar.go:12:2: undefined: Missing") {
		t.Fatalf("failure should carry the compiler error, got %+v", pkgs[0].Failures)
	}
}

func TestCompileErrLines(t *testing.T) {
	out := "# x/foo [x/foo.test]\nfoo/bar.go:12:2: undefined: Missing\n\nfoo/baz.go:3:1: syntax error\nFAIL\tx/foo [build failed]\n"
	got := compileErrLines(out, 10)
	want := []string{"foo/bar.go:12:2: undefined: Missing", "foo/baz.go:3:1: syntax error"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := compileErrLines(out, 1); len(got) != 1 {
		t.Fatalf("max not honored: %q", got)
	}
}

// The failure line shown is the test's own t.Error / t.Fatal line, even when the test
// printed output before it, and long lines are cut on a character boundary.
func TestFirstErrLine(t *testing.T) {
	out := "=== RUN   TestX\n    ── Upstream ──────────\n    │ status ok\n    upstream_sync_test.go:92: pushed=false refuse=\"no tag\"\n--- FAIL: TestX\n"
	if got := firstErrLine(out); got != `upstream_sync_test.go:92: pushed=false refuse="no tag"` {
		t.Errorf("got %q, want the t.Error line", got)
	}
	if got := firstErrLine("    panic: boom\n"); got != "panic: boom" {
		t.Errorf("got %q, want the first line when nothing is a t.Error line", got)
	}
	long := strings.Repeat("─", 200)
	if got := firstErrLine(long); !utf8.ValidString(got) || len([]rune(got)) != 120 {
		t.Errorf("clipped line: valid=%v runes=%d", utf8.ValidString(got), len([]rune(got)))
	}
}
