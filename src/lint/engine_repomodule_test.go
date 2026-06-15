package lint

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// stubRepoModule is a RepositoryModule built directly (not via the registry) so
// the test exercises engine dispatch without mutating the global registry.
type stubRepoModule struct {
	name     string
	findings []Finding
	err      error
	gotRoot  string
	desired  map[string]config.ToolPinConfig
}

func (s *stubRepoModule) Name() string        { return s.name }
func (s *stubRepoModule) DefaultEnabled() bool { return true }
func (s *stubRepoModule) CheckRepository(_ context.Context, root string) ([]Finding, error) {
	s.gotRoot = root
	return s.findings, s.err
}
func (s *stubRepoModule) SetToolchainDesired(d map[string]config.ToolPinConfig) { s.desired = d }

func TestRepositoryModuleDispatch(t *testing.T) {
	root := t.TempDir()
	stub := &stubRepoModule{
		name: "stub-repo",
		findings: []Finding{
			{Module: "stub-repo", Severity: SeverityCritical, Message: "repo-level problem"}, // File == ""
			{File: "a/b", Module: "stub-repo", Severity: SeverityInfo, Message: "coverage"},
		},
	}
	e := &Engine{
		RootDir:          root,
		RepoModules:      []RepositoryModule{stub},
		ToolchainDesired: map[string]config.ToolPinConfig{},
	}

	findings, stats, err := e.RunWithStats(context.Background(), nil)
	if err != nil {
		t.Fatalf("RunWithStats error: %v", err)
	}
	if stub.gotRoot != root {
		t.Errorf("CheckRepository root = %q, want %q", stub.gotRoot, root)
	}
	if stub.desired == nil {
		t.Errorf("SetToolchainDesired was not propagated to the repo module")
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %v", len(findings), findings)
	}
	// Repo-level finding with File == "" must survive intact.
	if findings[0].File != "" {
		t.Errorf("expected repo-level finding File==\"\", got %q", findings[0].File)
	}

	var repoStats *ModuleStats
	for i := range stats {
		if stats[i].Name == "stub-repo" {
			repoStats = &stats[i]
		}
	}
	if repoStats == nil {
		t.Fatalf("repo module missing from stats: %v", stats)
	}
	if repoStats.Findings != 2 || repoStats.Critical != 1 {
		t.Errorf("repo stats = %+v, want Findings=2 Critical=1", *repoStats)
	}
}

func TestRepositoryModuleErrorSurfaces(t *testing.T) {
	e := &Engine{
		RootDir:     t.TempDir(),
		RepoModules: []RepositoryModule{&stubRepoModule{name: "boom", err: context.Canceled}},
	}
	_, _, err := e.RunWithStats(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from failing repo module, got nil")
	}
}
