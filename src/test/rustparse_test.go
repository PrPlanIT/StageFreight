package test

import (
	"strings"
	"testing"
)

// Real `cargo test` output — a single crate: unit tests (2 pass, 1 fail),
// integration binary, and a doc-test binary.
const cargoSingleCrate = `   Compiling sfrusttest v0.1.0 (/work)
    Finished ` + "`test`" + ` profile [unoptimized + debuginfo] target(s) in 1.23s
     Running unittests src/lib.rs (target/debug/deps/sfrusttest-1a2b3c4d5e6f7890)

running 3 tests
test tests::passes_one ... ok
test tests::passes_two ... ok
test tests::fails_here ... FAILED

failures:

---- tests::fails_here stdout ----
thread 'tests::fails_here' panicked at src/lib.rs:11:5:
assertion ` + "`left == right`" + ` failed

failures:
    tests::fails_here

test result: FAILED. 2 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s

     Running tests/integration.rs (target/debug/deps/integration-abcdef1234567890)

running 1 test
test integration_works ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s

   Doc-tests sfrusttest

running 1 test
test src/lib.rs - add (line 3) ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.15s
`

func TestParseCargoTest_SingleCrate(t *testing.T) {
	var streamed int
	pkgs := parseCargoTest(strings.NewReader(cargoSingleCrate), func(PackageResult) { streamed++ })
	if len(pkgs) != 3 {
		t.Fatalf("want 3 test binaries, got %d: %+v", len(pkgs), pkgs)
	}
	if streamed != 3 {
		t.Errorf("onDone streamed %d times, want 3", streamed)
	}

	unit := pkgs[0]
	if unit.Rel != "sfrusttest" || unit.Status != StatusFailed || unit.Tests != 3 {
		t.Errorf("unit: got rel=%q status=%q tests=%d, want sfrusttest/failed/3", unit.Rel, unit.Status, unit.Tests)
	}
	if len(unit.Failures) != 1 || unit.Failures[0].Name != "tests::fails_here" {
		t.Errorf("unit failures = %+v, want [tests::fails_here]", unit.Failures)
	}
	if !strings.Contains(unit.Failures[0].Output, "panicked") {
		t.Errorf("unit failure output = %q, want it to capture the panic", unit.Failures[0].Output)
	}

	integ := pkgs[1]
	if integ.Rel != "integration" || integ.Status != StatusPassed || integ.Tests != 1 {
		t.Errorf("integration: got rel=%q status=%q tests=%d, want integration/passed/1", integ.Rel, integ.Status, integ.Tests)
	}

	doc := pkgs[2]
	if doc.Rel != "sfrusttest · doc" || doc.Status != StatusPassed || doc.Tests != 1 {
		t.Errorf("doc: got rel=%q status=%q tests=%d, want 'sfrusttest · doc'/passed/1", doc.Rel, doc.Status, doc.Tests)
	}
}

// A cargo WORKSPACE: two member crates, each with its own unit-test binary.
const cargoWorkspace = `   Compiling crate-a v0.1.0 (/work/crate-a)
   Compiling crate-b v0.1.0 (/work/crate-b)
    Finished test profile in 2.0s
     Running unittests crate-a/src/lib.rs (target/debug/deps/crate_a-1111111111111111)

running 1 test
test t::works ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s

     Running unittests crate-b/src/lib.rs (target/debug/deps/crate_b-2222222222222222)

running 2 tests
test t::works ... ok
test t::also ... ok

test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s
`

func TestParseCargoTest_Workspace(t *testing.T) {
	pkgs := parseCargoTest(strings.NewReader(cargoWorkspace), nil)
	if len(pkgs) != 2 {
		t.Fatalf("want 2 crate binaries, got %d: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Rel != "crate_a" || pkgs[0].Tests != 1 || pkgs[0].Status != StatusPassed {
		t.Errorf("crate_a: %+v", pkgs[0])
	}
	if pkgs[1].Rel != "crate_b" || pkgs[1].Tests != 2 || pkgs[1].Status != StatusPassed {
		t.Errorf("crate_b: %+v", pkgs[1])
	}
}

// A compile failure produces no test binaries — the caller then synthesizes a
// failure from the exit code + raw log, so nothing is silently swallowed.
func TestParseCargoTest_CompileErrorYieldsNoBinaries(t *testing.T) {
	const broken = `   Compiling broken v0.1.0 (/work)
error[E0425]: cannot find value ` + "`x`" + ` in this scope
 --> src/lib.rs:2:5
error: could not compile ` + "`broken`" + ` (lib) due to 1 previous error
`
	if pkgs := parseCargoTest(strings.NewReader(broken), nil); len(pkgs) != 0 {
		t.Errorf("compile error should yield 0 parsed binaries, got %d: %+v", len(pkgs), pkgs)
	}
}
