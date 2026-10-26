package cmd

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/forge"
)

// The release commands built the primary client from the remote URL and CI environment
// alone, so outside CI, behind an IP remote, `release create` failed with "could not
// detect forge from remote URL" and the project was empty. The declared primary supplies
// provider, endpoint and project.
func TestPrimaryForgeClient_DeclaredPrimaryOutsideCI(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required")
	}
	t.Setenv("CI_PROJECT_ID", "")
	t.Setenv("CI_PROJECT_PATH", "")
	repo := filepath.Join(t.TempDir(), "r")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "remote", "add", "origin", "ssh://git@10.30.1.123:2424/PrPlanIT/DisplayWizard.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cfg := cfgWithForge("gitlab", "https://gitlab.prplanit.com")
	cfg.Repos[0].Project = "PrPlanIT/DisplayWizard"

	fc, provider, err := primaryForgeClient(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	gl, ok := fc.(*forge.GitLabForge)
	if provider != forge.GitLab || !ok {
		t.Fatalf("got %s %T, want a GitLab client", provider, fc)
	}
	if gl.BaseURL != "https://gitlab.prplanit.com" || gl.ProjectID != "PrPlanIT/DisplayWizard" {
		t.Fatalf("client at %q project %q, want the declared endpoint and project", gl.BaseURL, gl.ProjectID)
	}
}
