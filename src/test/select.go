package test

import "github.com/PrPlanIT/StageFreight/src/config"

// Selection narrows a run to some packages (Go patterns or pytest paths) and/or tests
// (a go test -run regex or a pytest -k expression).
type Selection struct {
	Packages []string
	Run      string
}

// Empty reports whether the selection narrows nothing.
func (s Selection) Empty() bool { return len(s.Packages) == 0 && s.Run == "" }

// Narrow re-projects every go and python suite's argv with sel through its own flag
// builder, keeping declared flags, and lifts its coverage floor. Rust and script suites
// are returned as skipped.
func Narrow(suites []ResolvedSuite, sel Selection) (out []ResolvedSuite, skipped []string) {
	if sel.Empty() {
		return suites, nil
	}
	for _, rs := range suites {
		d := rs.decl
		if len(sel.Packages) > 0 {
			d.Packages = sel.Packages
		}
		if sel.Run != "" {
			d.Run = sel.Run
		}
		switch rs.Tool {
		case config.TestToolGo:
			rs.Argv = goArgv(d)
		case config.TestToolPython:
			rs.Argv = pytestArgv(d)
		default:
			skipped = append(skipped, rs.ID)
			continue
		}
		rs.CoverageMin = 0
		out = append(out, rs)
	}
	return out, skipped
}
