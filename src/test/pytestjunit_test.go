package test

import (
	"os"
	"path/filepath"
	"testing"
)

const junitSample = `<?xml version="1.0" encoding="utf-8"?>
<testsuites>
  <testsuite name="pytest" errors="0" failures="1" skipped="1" tests="4" time="1.234">
    <testcase classname="tests.test_ups_api_scraper" name="test_login" file="tests/test_ups_api_scraper.py" time="0.100"/>
    <testcase classname="tests.test_ups_api_scraper" name="test_get_measures" file="tests/test_ups_api_scraper.py" time="0.250"/>
    <testcase classname="tests.test_prometheus_api_exporter" name="test_single_collect" file="tests/test_prometheus_api_exporter.py" time="0.400">
      <failure message="AssertionError: 12 != 13">E   AssertionError: 12 != 13</failure>
    </testcase>
    <testcase classname="tests.test_skipped_mod" name="test_needs_hardware" file="tests/test_skipped_mod.py" time="0.001">
      <skipped message="no UPS available"/>
    </testcase>
  </testsuite>
</testsuites>`

// The Python transport projects per TEST MODULE, the analogue of go's per-package
// and rust's per-binary grouping: classname is the dotted module, file is the path a
// developer opens.
func TestParsePytestJUnit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.xml")
	if err := os.WriteFile(path, []byte(junitSample), 0o644); err != nil {
		t.Fatal(err)
	}

	var streamed []string
	pkgs := parsePytestJUnit(path, func(p PackageResult) { streamed = append(streamed, p.ImportPath) })

	if len(pkgs) != 3 {
		t.Fatalf("want 3 modules, got %d: %+v", len(pkgs), pkgs)
	}
	if len(streamed) != 3 {
		t.Errorf("onPkg must fire per module, got %v", streamed)
	}

	// Module 1: two passing tests, durations summed, first-appearance order.
	if pkgs[0].ImportPath != "tests.test_ups_api_scraper" || pkgs[0].Status != StatusPassed {
		t.Errorf("module 0: %+v", pkgs[0])
	}
	if pkgs[0].Tests != 2 {
		t.Errorf("module 0 tests = %d, want 2", pkgs[0].Tests)
	}
	if pkgs[0].Duration.Milliseconds() != 350 {
		t.Errorf("module 0 duration = %v, want 350ms (summed)", pkgs[0].Duration)
	}
	if pkgs[0].Rel != "tests/test_ups_api_scraper.py" {
		t.Errorf("Rel must be the file path, got %q", pkgs[0].Rel)
	}

	// Module 2: failing, with the leaf name and its captured output.
	if pkgs[1].Status != StatusFailed || len(pkgs[1].Failures) != 1 {
		t.Fatalf("module 1 must be failed with one failure: %+v", pkgs[1])
	}
	if pkgs[1].Failures[0].Name != "test_single_collect" {
		t.Errorf("failure name = %q", pkgs[1].Failures[0].Name)
	}
	if pkgs[1].Failures[0].Output == "" {
		t.Error("failure must carry its output")
	}

	// Module 3: only-skips reports skipped.
	if pkgs[2].Status != StatusSkipped {
		t.Errorf("skip-only module = %q, want skipped", pkgs[2].Status)
	}
}

// pytest emits a bare <testsuite> root in some configurations; both shapes parse.
func TestParsePytestJUnit_BareTestsuiteRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.xml")
	bare := `<testsuite name="pytest" tests="1">
	  <testcase classname="tests.test_x" name="test_a" file="tests/test_x.py" time="0.010"/>
	</testsuite>`
	if err := os.WriteFile(path, []byte(bare), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs := parsePytestJUnit(path, nil)
	if len(pkgs) != 1 || pkgs[0].Status != StatusPassed {
		t.Fatalf("bare testsuite root must parse, got %+v", pkgs)
	}
}

// A missing/unreadable report yields no packages — the runner turns that plus a
// non-zero exit into a synthetic failure (the cargo compile-error contract).
func TestParsePytestJUnit_MissingReport(t *testing.T) {
	if pkgs := parsePytestJUnit(filepath.Join(t.TempDir(), "nope.xml"), nil); pkgs != nil {
		t.Errorf("missing report must yield nil, got %+v", pkgs)
	}
}

func TestParsePytestCoverage(t *testing.T) {
	out := `
Name                     Stmts   Miss  Cover
--------------------------------------------
scraper.py                 120     30    75%
exporter.py                 80     20    75%
--------------------------------------------
TOTAL                      200     50    75%
`
	got, ok := parsePytestCoverage(out)
	if !ok || got != 75 {
		t.Errorf("parsePytestCoverage = %v, %v; want 75, true", got, ok)
	}
	if _, ok := parsePytestCoverage("no coverage here"); ok {
		t.Error("absent coverage must report ok=false")
	}
}
