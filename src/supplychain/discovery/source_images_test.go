package discovery

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestParseSourceImageRefs(t *testing.T) {
	const src = `package helper

// renovate: datasource=docker depName=docker.io/library/busybox
const DefaultHelperImage = "docker.io/library/busybox:1.38.0"

// renovate: datasource=github-releases depName=foo/bar
const Bar = "v1.2.3"

# renovate: datasource=docker depName=alpine
image: alpine:3.22.1@sha256:0123abcd # pinned

/* renovate: datasource=docker depName=busybox */
image = "ghcr.io/acme/busybox:1.0.0"

-- renovate: datasource=docker depName=postgres
SELECT 1;
`
	refs := parseSourceImageRefs([]byte(src))
	if len(refs) != 4 {
		t.Fatalf("want 4 docker annotations (github-releases ignored), got %d: %+v", len(refs), refs)
	}

	goRef := refs[0]
	if goRef.Skip != "" || goRef.Ref != "docker.io/library/busybox:1.38.0" || goRef.Line != 4 {
		t.Errorf("go annotation = %+v", goRef)
	}
	if line := strings.Split(src, "\n")[goRef.Line-1]; !strings.HasPrefix(line[goRef.Column:], "1.38.0\"") {
		t.Errorf("column %d does not point at the tag in %q", goRef.Column, line)
	}

	if hash := refs[1]; hash.Skip != "" || hash.Ref != "alpine:3.22.1@sha256:0123abcd" || hash.Line != 10 {
		t.Errorf("# annotation = %+v", hash)
	}

	// depName must start the reference, not end a longer image name.
	if longer := refs[2]; longer.Skip == "" || longer.Line != 12 {
		t.Errorf("busybox inside ghcr.io/acme/busybox must be skipped on the annotation line, got %+v", longer)
	}
	if missing := refs[3]; !strings.Contains(missing.Skip, "postgres:<tag>") || missing.Line != 15 {
		t.Errorf("annotation without a reference on the next line must be skipped, got %+v", missing)
	}
}

func TestParseSourceImageRefs_AnnotationOnLastLine(t *testing.T) {
	refs := parseSourceImageRefs([]byte("# renovate: datasource=docker depName=alpine"))
	if len(refs) != 1 || refs[0].Skip == "" || refs[0].Line != 1 {
		t.Fatalf("want one skipped annotation on line 1, got %+v", refs)
	}
}

func writeSource(t *testing.T, body string) lint.FileInfo {
	t.Helper()
	dir := t.TempDir()
	abs := filepath.Join(dir, "helper.go")
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return lint.FileInfo{Path: "helper.go", AbsPath: abs}
}

// Without dependency.scope.source_images nothing is read from annotations.
func TestSourceImages_OffDiscoversNothing(t *testing.T) {
	file := writeSource(t, "// renovate: datasource=docker depName=busybox\nconst Image = \"busybox:1.38.0\"\n")
	deps, err := Resolve(context.Background(), nil, nil, []lint.FileInfo{file}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 0 {
		t.Fatalf("source_images off must discover nothing, got %+v", deps)
	}
}

// A skipped annotation surfaces as an unresolved dependency carrying the reason.
func TestSourceImages_SkippedAnnotationIsUnresolved(t *testing.T) {
	file := writeSource(t, "// renovate: datasource=docker depName=busybox\nconst Image = \"alpine:3.22.1\"\n")
	deps, err := Resolve(context.Background(), map[string]any{"source_images": true}, nil, []lint.FileInfo{file}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0].Ecosystem != supplychain.EcosystemSourceImage || deps[0].ResolutionError == "" || deps[0].Line != 1 {
		t.Fatalf("want one unresolved source-image dependency on line 1, got %+v", deps)
	}
}

// An annotated reference resolves through the image registry like a Dockerfile base.
func TestSourceImages_ResolvesLikeBaseImage(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	image := host + "/library/busybox"
	for _, v := range []string{"1.36.0", "1.36.1", "1.37.0"} {
		tag, err := name.NewTag(image+":"+v, name.WeakValidation)
		if err != nil {
			t.Fatal(err)
		}
		if err := remote.Write(tag, empty.Image); err != nil {
			t.Fatal(err)
		}
	}

	line := "\tDefaultHelperImage = \"" + image + ":1.36.0\" // helper"
	file := writeSource(t, "package helper\n\n// renovate: datasource=docker depName="+image+"\n"+line+"\n")
	deps, err := Resolve(context.Background(), map[string]any{"source_images": true}, nil, []lint.FileInfo{file}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 {
		t.Fatalf("want one dependency, got %+v", deps)
	}
	d := deps[0]
	if d.Ecosystem != supplychain.EcosystemSourceImage || d.File != "helper.go" || d.Line != 4 || d.Current != "1.36.0" {
		t.Errorf("dependency = %+v", d)
	}
	if d.Column != strings.Index(line, "1.36.0") {
		t.Errorf("column = %d, want %d", d.Column, strings.Index(line, "1.36.0"))
	}
	if d.LatestEligible != "1.36.1" || d.Latest != "1.37.0" {
		t.Errorf("eligible/latest = %s/%s, want 1.36.1/1.37.0", d.LatestEligible, d.Latest)
	}
}
