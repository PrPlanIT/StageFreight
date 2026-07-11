package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestBuildNpmReplacement(t *testing.T) {
	d := func(latest string) supplychain.Dependency {
		return supplychain.Dependency{Name: "lodash", Ecosystem: supplychain.EcosystemNpm, Current: "4.17.10", Latest: latest, File: "package.json"}
	}
	cases := []struct {
		name, line, latest, wantLine string
		wantSkip                     bool
	}{
		{"caret preserved", `    "lodash": "^4.17.10",`, "4.17.21", `    "lodash": "^4.17.21",`, false},
		{"tilde preserved", `    "lodash": "~4.17.10"`, "4.17.21", `    "lodash": "~4.17.21"`, false},
		{"exact", `    "lodash": "4.17.10",`, "4.17.21", `    "lodash": "4.17.21",`, false},
		{"git spec skipped", `    "lodash": "git+https://github.com/x/y.git",`, "4.17.21", "", true},
		{"workspace skipped", `    "lodash": "workspace:*",`, "4.17.21", "", true},
		{"tag skipped", `    "lodash": "latest",`, "4.17.21", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, reason := buildNpmReplacement(d(tc.latest), tc.line)
			if tc.wantSkip {
				if reason == "" {
					t.Errorf("want skip, got %q", got)
				}
				return
			}
			if reason != "" {
				t.Fatalf("unexpected skip: %s", reason)
			}
			if got != tc.wantLine {
				t.Errorf("newLine = %q, want %q", got, tc.wantLine)
			}
		})
	}
}

func TestNpmIsAutoUpdatable(t *testing.T) {
	d := supplychain.Dependency{Name: "lodash", Ecosystem: supplychain.EcosystemNpm, File: "package.json", Current: "4.17.10", Latest: "4.17.21"}
	cands, skipped := FilterUpdateCandidates([]supplychain.Dependency{d}, UpdateConfig{}, nil)
	if len(cands) != 1 {
		t.Fatalf("npm dep must be a candidate now, got skipped: %+v", skipped)
	}
}
