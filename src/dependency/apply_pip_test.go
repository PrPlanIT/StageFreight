package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestBuildPipReplacement(t *testing.T) {
	dep := func(cur, latest, file string) supplychain.Dependency {
		return supplychain.Dependency{Name: "django", Ecosystem: supplychain.EcosystemPip, Current: cur, Latest: latest, File: file}
	}
	cases := []struct {
		name, line string
		d          supplychain.Dependency
		wantLine   string
		wantSkip   bool
	}{
		{"exact bump", "django==3.2.1", dep("3.2.1", "3.2.4", "requirements.txt"), "django==3.2.4", false},
		{"exact with marker preserved", `django==3.2.1 ; python_version >= "3.6"`, dep("3.2.1", "3.2.4", "requirements.txt"), `django==3.2.4 ; python_version >= "3.6"`, false},
		{"range not bumped", "django>=3.2", dep("3.2", "3.2.4", "requirements.txt"), "", true},
		{"pipfile skipped", `django = "==3.2.1"`, dep("3.2.1", "3.2.4", "Pipfile"), "", true},
		{"no change", "django==3.2.4", dep("3.2.4", "3.2.4", "requirements.txt"), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, reason := buildPipReplacement(tc.d, tc.line)
			if tc.wantSkip {
				if reason == "" {
					t.Errorf("want skip, got newLine %q", got)
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

// pip is now auto-updatable end-to-end (candidate → apply).
func TestPipIsAutoUpdatable(t *testing.T) {
	d := supplychain.Dependency{Name: "django", Ecosystem: supplychain.EcosystemPip, File: "requirements.txt", Current: "3.2.1", Latest: "3.2.4"}
	cands, skipped := FilterUpdateCandidates([]supplychain.Dependency{d}, UpdateConfig{}, nil)
	if len(cands) != 1 {
		t.Fatalf("pip dep must be a candidate now, got skipped: %+v", skipped)
	}
}
