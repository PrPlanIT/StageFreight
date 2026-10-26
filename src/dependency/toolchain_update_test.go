package dependency

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func stubArtifactDigest(t *testing.T) {
	t.Helper()
	orig := fetchArtifactSHA256
	fetchArtifactSHA256 = func(tool, ver string) (string, error) { return "deadbeef", nil }
	t.Cleanup(func() { fetchArtifactSHA256 = orig })
}

func toolchainRepoFiles() map[string]string {
	return map[string]string{
		"go.mod": "module example.com/x\n\ngo 1.24\n",
		".stagefreight.yml": "version: 1\ntoolchains:\n  want:\n    trivy: \"0.x\"\n" +
			"    kubectl: \"1.34.2\" # cluster line\n",
	}
}

func outdatedToolchainDeps() []supplychain.Dependency {
	return []supplychain.Dependency{
		{Name: "trivy", Current: "0.69.3", Latest: "0.76.1", Ecosystem: supplychain.EcosystemToolchain, File: ".stagefreight.yml", AvailableVersions: []string{"0.69.3", "0.76.1"}},
		{Name: "kubectl", Current: "1.34.2", Latest: "1.34.5", Ecosystem: supplychain.EcosystemToolchain, File: ".stagefreight.yml", AvailableVersions: []string{"1.34.2", "1.34.5"}},
	}
}

func appliedNames(r *UpdateResult) map[string]bool {
	m := map[string]bool{}
	for _, a := range r.Applied {
		m[a.Dep.Name] = true
	}
	return m
}

// TestUpdate_ToolchainsFollowUpdateNotRemediate pins that toolchains move under
// remediate: false (DryRun) and stay put only when toolchains.update says so.
func TestUpdate_ToolchainsFollowUpdateNotRemediate(t *testing.T) {
	stubArtifactDigest(t)
	ctx := context.Background()

	t.Run("remediate false still moves toolchains", func(t *testing.T) {
		repo := initTestRepo(t, toolchainRepoFiles())
		res, err := Update(ctx, UpdateConfig{RootDir: repo, DryRun: true, Ecosystems: []string{supplychain.EcosystemToolchain}}, outdatedToolchainDeps())
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if got := appliedNames(res); !got["trivy"] || !got["kubectl"] {
			t.Fatalf("both toolchains must move under remediate: false, applied = %v", got)
		}
		lock, _ := os.ReadFile(filepath.Join(repo, ".stagefreight", "toolchains.lock"))
		if !strings.Contains(string(lock), "0.76.1") || !strings.Contains(string(lock), "1.34.5") {
			t.Fatalf("lock must record both moves, got:\n%s", lock)
		}
		cfg, _ := os.ReadFile(filepath.Join(repo, ".stagefreight.yml"))
		if !strings.Contains(string(cfg), `kubectl: "1.34.5" # cluster line`) {
			t.Fatalf("exact pin must be bumped with its comment kept, got:\n%s", cfg)
		}
		if !strings.Contains(string(cfg), `trivy: "0.x"`) {
			t.Fatalf("a wildcard constraint must stay as written, got:\n%s", cfg)
		}
	})

	t.Run("update false holds a tool even with remediate on", func(t *testing.T) {
		repo := initTestRepo(t, toolchainRepoFiles())
		held := func(tool string) string {
			if tool == "kubectl" {
				return "toolchains update: false"
			}
			return ""
		}
		res, err := Update(ctx, UpdateConfig{RootDir: repo, DryRun: false, Ecosystems: []string{supplychain.EcosystemToolchain}, ToolchainHold: held}, outdatedToolchainDeps())
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		got := appliedNames(res)
		if !got["trivy"] || got["kubectl"] {
			t.Fatalf("trivy must move and kubectl must be held, applied = %v", got)
		}
		var heldSkip bool
		for _, s := range res.Skipped {
			if s.Dep.Name == "kubectl" && s.Category == SkipToolchainHeld {
				heldSkip = true
			}
		}
		if !heldSkip {
			t.Fatalf("held kubectl must be reported as %s, skipped = %+v", SkipToolchainHeld, res.Skipped)
		}
		cfg, _ := os.ReadFile(filepath.Join(repo, ".stagefreight.yml"))
		if !strings.Contains(string(cfg), `kubectl: "1.34.2"`) {
			t.Fatalf("held kubectl pin must not change, got:\n%s", cfg)
		}
	})
}

func TestRewriteVersion(t *testing.T) {
	cases := []struct{ name, line, key, want string }{
		{"scalar", `    trivy: "0.69.3"`, "trivy", `    trivy: "0.76.1"`},
		{"scalar with comment", `    kubectl: "1.34.2" # cluster line`, "kubectl", `    kubectl: "1.34.5" # cluster line`},
		{"block version line", `      version: 0.8.3`, "version", `      version: "0.8.4"`},
		{"flow map keeps siblings", `    cosign: {version: "3.0.6", update: true} # signing`, "cosign", `    cosign: {version: "3.0.7", update: true} # signing`},
		{"flow map version not first", `    cosign: {update: true, version: 3.0.6}`, "cosign", `    cosign: {update: true, version: "3.0.7"}`},
	}
	vers := map[string]string{"trivy": "0.76.1", "kubectl": "1.34.5", "version": "0.8.4", "cosign": "3.0.7"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteVersion(tc.line, tc.key, vers[tc.key]); got != tc.want {
				t.Errorf("rewriteVersion(%q) =\n  %q\nwant\n  %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestFlowMapVersion(t *testing.T) {
	cases := map[string]string{
		`{version: "3.0.6"}`:                "3.0.6",
		`{version: 1.34.x, update: false}`:  "1.34.x",
		`{update: false, version: '2.8.x'}`: "2.8.x",
		`"0.x"`:                             `"0.x"`,
		`{update: false}`:                   "",
	}
	for in, want := range cases {
		if got := flowMapVersion(in); got != want {
			t.Errorf("flowMapVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
