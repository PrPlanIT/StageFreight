package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// A version pin downloaded from a source the resolver can't query must surface as
// unresolved. Silently dropping it hid a Docker CLI that never updated and shipped
// stdlib CVEs.
func TestCheckTools_UntrackedDownloadIsUnresolved(t *testing.T) {
	dir := t.TempDir()
	df := filepath.Join(dir, "Dockerfile")
	content := `FROM alpine:3.24
ENV DOCKER_VERSION=29.3.1 \
    APP_VERSION=1.2.3
RUN wget -qO /tmp/docker.tgz "https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz"
LABEL version=$APP_VERSION
`
	if err := os.WriteFile(df, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := NewResolver().checkTools(context.Background(), lint.FileInfo{Path: "Dockerfile", AbsPath: df}, []supplychain.PinnedTool{
		{EnvName: "DOCKER_VERSION", Version: "29.3.1", Line: 2},
		{EnvName: "APP_VERSION", Version: "1.2.3", Line: 3},
	})

	if len(deps) != 1 {
		t.Fatalf("want only the downloaded pin reported, got %d: %+v", len(deps), deps)
	}
	d := deps[0]
	if d.Name != "docker" || d.Current != "29.3.1" || d.Binding != "DOCKER_VERSION" || d.Line != 2 {
		t.Errorf("unexpected dependency: %+v", d)
	}
	if !strings.Contains(d.ResolutionError, "download.docker.com") {
		t.Errorf("ResolutionError must name the download source, got %q", d.ResolutionError)
	}
	if d.Latest != "" {
		t.Errorf("an unresolved pin must not claim a latest version, got %q", d.Latest)
	}
}

func TestDownloadURLFor(t *testing.T) {
	dir := t.TempDir()
	df := filepath.Join(dir, "Dockerfile")
	content := `RUN wget https://example.com/tool-$TOOL_VERSION.tgz
RUN wget https://example.com/other-${TOOL_VERSION_SUFFIX}.tgz
`
	if err := os.WriteFile(df, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"TOOL_VERSION":        "https://example.com/tool-$TOOL_VERSION.tgz",
		"TOOL_VERSION_SUFFIX": "https://example.com/other-${TOOL_VERSION_SUFFIX}.tgz",
		"OTHER_VERSION":       "",
	}
	for name, want := range cases {
		if got := downloadURLFor(df, name); got != want {
			t.Errorf("downloadURLFor(%s) = %q, want %q", name, got, want)
		}
	}
}
