package docker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

func TestCacheExportIgnoresErrorsAndImportDoesNot(t *testing.T) {
	cases := []struct {
		ref  build.CacheRef
		want string
	}{
		{build.CacheRef{Type: "registry", Ref: "r/x:y", Mode: "max", Direction: "export"}, "type=registry,ref=r/x:y,mode=max,ignore-error=true"},
		{build.CacheRef{Type: "local", Ref: "/d", Mode: "max", Direction: "export"}, "type=local,dest=/d,mode=max,ignore-error=true"},
		{build.CacheRef{Type: "registry", Ref: "r/x:y", Direction: "import"}, "type=registry,ref=r/x:y"},
	}
	for _, c := range cases {
		if got := c.ref.Flag(); got != c.want {
			t.Errorf("Flag() = %q, want %q", got, c.want)
		}
	}
}

func TestCacheExportFailureReadsWhatBuildKitIgnored(t *testing.T) {
	ok := "#5 exporting cache to registry\n#5 sending cache export 0.2s done\n#5 DONE 0.2s\n"
	if got := cacheExportFailure(ok); got != "" {
		t.Errorf("clean export reported %q", got)
	}
	failed := "#5 exporting cache to registry\n#5 sending cache export 0.2s done\n#5 ERROR: error writing layer blob: denied\n"
	if got, want := cacheExportFailure(failed), "error writing layer blob: denied"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A first build has no cache to import yet; its export can still succeed.
	missingImport := "#3 importing cache manifest from cr/x:y\n#3 ERROR: failed to configure registry cache importer: cr/x:y: not found\n" + ok
	if got := cacheExportFailure(missingImport); got != "" {
		t.Errorf("missing import reported as export failure: %q", got)
	}
}

// Each build in a repo keeps its own cache: sharing one, the last export replaces the rest.
func TestBuildsInARepoDoNotShareACache(t *testing.T) {
	t.Setenv(paths.CacheRootEnv, t.TempDir())
	cfg := config.LocalCacheConfig{}
	_, image := localFlags(cacheScope("repo", "image"), cfg)
	_, login := localFlags(cacheScope("repo", "login"), cfg)
	if len(image) != 1 || len(login) != 1 || image[0].Ref == login[0].Ref {
		t.Fatalf("image=%v login=%v", image, login)
	}
	a, b := BuildCacheTag("cache", cacheScope("repo", "image"), "main"), BuildCacheTag("cache", cacheScope("repo", "login"), "main")
	if a.String() == b.String() {
		t.Errorf("both builds export to %s", a)
	}
}

func TestPromoteDiscardsAnIncompleteExport(t *testing.T) {
	dir := t.TempDir()
	writeGeneration(t, filepath.Join(dir, localCurrent), 10)
	next := localNext(dir)
	if err := os.MkdirAll(filepath.Join(next, "ingest"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := promoteLocalCache(dir, config.LocalRetention{})
	if res.Promoted {
		t.Fatalf("promoted an export with no index: %+v", res)
	}
	if _, err := os.Stat(next); err == nil {
		t.Error("the incomplete export must be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, localCurrent, "index.json")); err != nil {
		t.Error("the current generation must survive a failed export")
	}
}

func TestPlanningRemovesExportsOfDeadRuns(t *testing.T) {
	t.Setenv(paths.CacheRootEnv, t.TempDir())
	cfg := config.LocalCacheConfig{}
	dir := LocalCacheDir("repo", cfg)
	writeGeneration(t, filepath.Join(dir, localNextPrefix+"dead-11"), 10)
	writeGeneration(t, filepath.Join(dir, localCurrent), 10)
	if from, _ := localFlags("repo", cfg); len(from) != 1 {
		t.Fatalf("current generation not imported: %v", from)
	}
	if _, err := os.Stat(filepath.Join(dir, localNextPrefix+"dead-11")); err == nil {
		t.Error("the dead run's export must be removed")
	}
}

func TestFailedCacheLoginDropsOnlyTheRegistryExport(t *testing.T) {
	steps := []build.BuildStep{{
		Name: "image",
		CacheTo: []build.CacheRef{
			{Type: "local", Ref: "/d", Direction: "export"},
			{Type: "registry", Ref: "cr.example/x:y", Direction: "export"},
		},
		Registries: []build.RegistryTarget{{URL: "cr.example", Credentials: "SF_TEST_MISSING_CRED"}},
	}}
	loginForCacheExport(context.Background(), steps)
	if len(steps[0].CacheTo) != 1 || steps[0].CacheTo[0].Type != "local" {
		t.Errorf("CacheTo = %v, want the local export alone", steps[0].CacheTo)
	}
}

func TestLocalBuildsKeepOnlyLocalExports(t *testing.T) {
	refs := []build.CacheRef{{Type: "local", Ref: "/d"}, {Type: "registry", Ref: "cr/x:y"}}
	if got := exportableCacheTo(refs, true); len(got) != 1 || got[0].Type != "local" {
		t.Errorf("local build: %v", got)
	}
	if got := exportableCacheTo(refs, false); len(got) != 2 {
		t.Errorf("CI build: %v", got)
	}
}

func TestCacheNotesRenderInTheBuildsPanelByName(t *testing.T) {
	var buf bytes.Buffer
	sec := output.NewSection(&buf, "Image", 0, false)
	plan := &build.BuildPlan{Steps: []build.BuildStep{
		{Name: "image"},
		{Name: "login", CacheNote: "cache not saved to registry: denied"},
	}}
	renderCacheNotes(sec, plan, []build.StepResult{{CacheNote: "cache not saved: no space"}, {}}, false)
	got := buf.String()
	for _, want := range []string{"image  cache not saved: no space", "login  cache not saved to registry: denied"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
