package governance

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// hiddenRepoForge is a forge that cannot see the repo at all: GitLab answers a project
// the caller has no access to with 404, for the project and for every file in it.
type hiddenRepoForge struct{}

func (hiddenRepoForge) GetFileContent(_, path, _ string) ([]byte, error) {
	return nil, fmt.Errorf("%w: %s", ErrFileNotFound, path)
}

func (hiddenRepoForge) DefaultBranch(string) (string, error) {
	return "", errors.New("GET /projects/HomeLabHD%2Fapt-cacher-ng: 404 Project Not Found")
}

// A repo the reader cannot see is not an empty repo. A reconcile run without credentials
// planned all 735 governed files across the fleet as creates; every one must instead be
// unreadable, carrying why, and the plan view must say so rather than report the repo
// as missing or unchanged.
func TestUnreadableRepoIsNeverPlannedAsCreate(t *testing.T) {
	gov := &GovernanceConfig{
		Profiles: ProfileList{{
			ID:          "hlhd-docker",
			Credentials: "GITLAB_HOMELABHD",
			Config: map[string]any{
				"version": 1,
				"forges": map[string]any{
					"gitlab": map[string]any{"provider": "gitlab", "url": "https://gitlab.prplanit.com"},
				},
			},
			Repos: ProfileCatalog{{ID: "apt-cacher-ng", At: "HomeLabHD/apt-cacher-ng"}},
		}},
	}

	plans, err := PlanDistribution(gov, fakeLoader{}, nil, hiddenRepoForge{},
		PresetSourceInfo{Provider: "gitlab", ForgeURL: "https://gitlab.prplanit.com", ProjectID: "PrPlanIT/MaintenancePolicy"},
		"PrPlanIT/MaintenancePolicy")
	requireNoError(t, err)

	if len(plans) != 1 || len(plans[0].Files) == 0 {
		t.Fatalf("expected one plan with files, got %+v", plans)
	}
	for _, f := range plans[0].Files {
		if f.Action != "unreadable" {
			t.Errorf("%s: action %q, want unreadable", f.Path, f.Action)
		}
		if f.ReadErr == nil || !strings.Contains(f.ReadErr.Error(), "404") {
			t.Errorf("%s: ReadErr %v does not carry the forge's answer", f.Path, f.ReadErr)
		}
	}

	if got := repoPlanState(plans[0]); got != "unreadable" {
		t.Errorf("repo state %q, want unreadable", got)
	}

	var out bytes.Buffer
	RenderPlanView(&out, PlanViewData{
		Config:   PlanViewConfig{Mode: "dry-run", Source: "PrPlanIT/MaintenancePolicy"},
		Profiles: gov.Profiles,
		Plans:    map[string]DistributionPlan{plans[0].Repo: plans[0]},
	})
	view := out.String()
	for _, want := range []string{"unreadable · 1 repo", "404 Project Not Found", "repos unreadable", "files create        0"} {
		if !strings.Contains(view, want) {
			t.Errorf("plan view lacks %q:\n%s", want, view)
		}
	}
}
