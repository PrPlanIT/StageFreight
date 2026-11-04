package workflow

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// trunk is the trunk model: main lands through merge requests and carries every release
// kind, feature branches are pushed to directly, release lines carry patches.
func trunk() *config.Config {
	return &config.Config{
		Git: config.GitConfig{Branches: map[string]string{
			"main": "^main$", "feature": "^feature/", "release": `^release-\d+\.\d+$`,
		}},
		Workflow: config.WorkflowConfig{
			Branches: config.OrderedBranchClasses{
				{ID: "main", Match: "main", Landing: config.LandingMergeRequest},
				{ID: "feature", Match: "feature", Lifetime: config.LifetimeShort, Landing: config.LandingDirect},
				{ID: "release", Match: "release", Lifetime: config.LifetimeOnDemand, CreatedFrom: "tag", Landing: config.LandingMergeRequest},
			},
			Promote: config.OrderedPromotions{
				{ID: "land", From: "feature", To: "main", Method: config.PromoteMergeRequest},
			},
			Release: config.WorkflowRelease{From: map[string][]string{
				"main":    {"prerelease", "major", "minor", "patch"},
				"release": {"prerelease", "patch"},
			}},
		},
	}
}

func TestClassOf(t *testing.T) {
	cfg := trunk()
	for branch, want := range map[string]string{"main": "main", "feature/x": "feature", "release-0.16": "release", "dev": ""} {
		c, ok := ClassOf(cfg, branch)
		if got := c.ID; ok != (want != "") || got != want {
			t.Errorf("ClassOf(%q) = %q, %v; want %q", branch, got, ok, want)
		}
	}
}

func TestLanding(t *testing.T) {
	cfg := trunk()
	f := Landing(cfg, "main", "commit")
	if f == nil || f.Evaluate || f.Class != "main" {
		t.Fatalf("a direct commit to main must be refused, got %+v", f)
	}
	if !strings.Contains(f.Message, "through a merge request") || !strings.Contains(f.Teach, "feature branch (matching ^feature/)") {
		t.Errorf("finding must explain the rule and the way through it: %+v", f)
	}
	for _, branch := range []string{"feature/x", "dev", ""} {
		if f := Landing(cfg, branch, "push"); f != nil {
			t.Errorf("%q is not a merge-request class, got %+v", branch, f)
		}
	}

	cfg.Workflow.Branches[0].Enforcement = config.EnforcementEvaluate
	if f := Landing(cfg, "main", "push"); f == nil || !f.Evaluate {
		t.Errorf("evaluate reports without refusing, got %+v", f)
	}
	cfg.Workflow.Branches[0].Enforcement = config.EnforcementOff
	if f := Landing(cfg, "main", "push"); f != nil {
		t.Errorf("off governs nothing, got %+v", f)
	}
	if f := Landing(&config.Config{}, "main", "commit"); f != nil {
		t.Errorf("no workflow: block governs nothing, got %+v", f)
	}
}

func TestKind(t *testing.T) {
	for tag, want := range map[string]string{
		"v1.0.0": "major", "v0.16.0": "minor", "v0.15.1": "patch", "v0.16.0-rc.1": "prerelease", "1.2.3": "patch",
	} {
		if got, ok := Kind(tag); !ok || got != want {
			t.Errorf("Kind(%q) = %q, %v; want %q", tag, got, ok, want)
		}
	}
	if _, ok := Kind("latest-dev"); ok {
		t.Error("a non-version tag has no kind")
	}
}

func TestRelease(t *testing.T) {
	cfg := trunk()
	cases := []struct {
		tag      string
		branches []string
		refused  bool
		evaluate bool
	}{
		{"v0.16.0", []string{"main"}, false, false},
		{"v0.16.0", []string{"feature/x", "main"}, false, false},
		{"v0.15.1", []string{"release-0.15"}, false, false},
		{"v0.16.0", []string{"release-0.16"}, true, false}, // a minor isn't cut from a release line
		{"v0.16.0", []string{"feature/x"}, true, false},
		{"v0.16.0", nil, true, false},
		{"latest-dev", []string{"feature/x"}, false, false}, // not a version: not a release
	}
	for _, c := range cases {
		f := Release(cfg, c.tag, c.branches)
		if (f != nil) != c.refused {
			t.Errorf("Release(%s on %v) = %+v; refused want %v", c.tag, c.branches, f, c.refused)
			continue
		}
		if f != nil && f.Evaluate != c.evaluate {
			t.Errorf("Release(%s on %v).Evaluate = %v", c.tag, c.branches, f.Evaluate)
		}
	}
	f := Release(cfg, "v0.16.0", []string{"feature/x"})
	if !strings.Contains(f.Message, "minor release") || !strings.Contains(f.Message, "only from main") {
		t.Errorf("finding must name the kind and the allowed classes: %s", f.Message)
	}

	cfg.Workflow.Branches[1].Enforcement = config.EnforcementEvaluate
	if f := Release(cfg, "v0.16.0", []string{"feature/x"}); f == nil || !f.Evaluate {
		t.Errorf("on an evaluate class the finding is reported, not refused: %+v", f)
	}
}

func TestPromotion(t *testing.T) {
	cfg := trunk()
	cfg.Workflow.Promote = append(cfg.Workflow.Promote, config.Promotion{ID: "backport", From: "main", To: "release", Method: config.PromoteCherryPick})
	if p, err := Promotion(cfg, "feature/x", ""); err != nil || p.ID != "land" {
		t.Errorf("the one promotion out of feature: %+v, %v", p, err)
	}
	if p, err := Promotion(cfg, "main", "backport"); err != nil || p.ID != "backport" {
		t.Errorf("named: %+v, %v", p, err)
	}
	for branch, want := range map[string]string{"release-0.16": "no promotion starts from the release class", "dev": "belongs to no workflow.branches class"} {
		if _, err := Promotion(cfg, branch, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Promotion(%s) err = %v, want %q", branch, err, want)
		}
	}
	cfg.Workflow.Promote = append(cfg.Workflow.Promote, config.Promotion{ID: "land-release", From: "feature", To: "release", Method: config.PromoteMergeRequest})
	if _, err := Promotion(cfg, "feature/x", ""); err == nil || !strings.Contains(err.Error(), "name one (land, land-release)") {
		t.Errorf("several out of one class must be named: %v", err)
	}
	if _, err := Promotion(cfg, "feature/x", "nope"); err == nil {
		t.Error("an unknown id must be refused")
	}
}
