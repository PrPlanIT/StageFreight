package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

// TestResolve_DiscoversToolchainConstraints pins that Resolve resolves the toolchains it
// is given. Without them no toolchain is discovered, no lock is ever written, and every
// wildcard silently runs the registry default.
func TestResolve_DiscoversToolchainConstraints(t *testing.T) {
	t.Chdir(t.TempDir()) // no .stagefreight/toolchains.lock
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"tag_name":"v0.76.1"},{"tag_name":"v0.69.3"}]`))
	}))
	defer srv.Close()

	opts := map[string]any{"registries": map[string]any{"github": map[string]any{"url": srv.URL}}}
	files := []lint.FileInfo{{Path: ".stagefreight.yml"}}
	want := map[string]config.ToolConstraint{"trivy": {Constraint: "0.x"}}

	deps, err := Resolve(context.Background(), opts, want, files, nil, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var found bool
	for _, d := range deps {
		if d.Name == "trivy" {
			found = true
			if d.Current != "0.76.1" {
				t.Errorf("trivy 0.x resolved to %q, want 0.76.1", d.Current)
			}
		}
	}
	if !found {
		t.Fatalf("trivy not discovered; deps = %+v", deps)
	}

	none, err := Resolve(context.Background(), opts, nil, files, nil, "")
	if err != nil {
		t.Fatalf("Resolve without toolchains: %v", err)
	}
	for _, d := range none {
		if d.Name == "trivy" {
			t.Errorf("trivy discovered without a toolchains constraint")
		}
	}
}
