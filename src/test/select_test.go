package test

import (
	"reflect"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// A selection narrows go and python suites through their own flag builders (declared
// flags kept, coverage floor lifted) and reports suites it cannot narrow.
func TestNarrow(t *testing.T) {
	tr := true
	min := 40.0
	goDecl := config.TestSuite{ID: "unit", Tool: config.TestToolGo, Race: &tr, Vet: "all", Coverage: &tr, CoverageMin: &min}
	pyDecl := config.TestSuite{ID: "py", Tool: config.TestToolPython, Markers: []string{"fast"}}
	rustDecl := config.TestSuite{ID: "crate", Tool: config.TestToolRust}
	suites := []ResolvedSuite{
		{ID: "unit", Tool: config.TestToolGo, Argv: goArgv(goDecl), CoverageMin: 40, decl: goDecl},
		{ID: "py", Tool: config.TestToolPython, Argv: pytestArgv(pyDecl), decl: pyDecl},
		{ID: "crate", Tool: config.TestToolRust, Argv: []string{"cargo", "test"}, decl: rustDecl},
		{ID: "go-default-bin", Tool: config.TestToolGo, Argv: []string{"go", "test", "./..."}, Synthesized: true,
			decl: config.TestSuite{ID: "go-default-bin", Tool: config.TestToolGo}},
	}

	got, skipped := Narrow(suites, Selection{Packages: []string{"./src/runner/..."}, Run: "TestCgroup"})
	if !reflect.DeepEqual(skipped, []string{"crate"}) {
		t.Fatalf("skipped = %v, want [crate]", skipped)
	}
	want := map[string][]string{
		"unit":           {"go", "test", "-run", "TestCgroup", "-race", "-vet=all", "-cover", "./src/runner/..."},
		"py":             {"python", "-m", "pytest", "-k", "TestCgroup", "-m", "fast", "./src/runner/..."},
		"go-default-bin": {"go", "test", "-run", "TestCgroup", "./src/runner/..."},
	}
	for _, rs := range got {
		if !reflect.DeepEqual(rs.Argv, want[rs.ID]) {
			t.Errorf("%s argv = %v\n want %v", rs.ID, rs.Argv, want[rs.ID])
		}
		if rs.CoverageMin != 0 {
			t.Errorf("%s: a narrowed run must not gate on the whole-suite coverage floor", rs.ID)
		}
	}

	if same, sk := Narrow(suites, Selection{}); len(sk) != 0 || !reflect.DeepEqual(same, suites) {
		t.Error("an empty selection must leave the suites untouched")
	}
}
