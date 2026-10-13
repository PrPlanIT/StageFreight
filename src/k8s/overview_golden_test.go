package k8s

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

// The rendered overview, byte for byte, for a small cluster with every section. Any
// change to the page's layout shows up as a diff of testdata/overview.golden.md, so it
// is reviewed rather than discovered on a live page. Regenerate with: go test -update
func TestOverviewGolden(t *testing.T) {
	out := RenderOverview(&DiscoveryResult{
		Cluster: "lab",
		Apps: []AppRecord{{
			Key: AppKey{Namespace: "pantry", Identity: "toaster"}, FriendlyName: "Toaster", Category: "Kitchen",
			Components:    []ComponentRef{{Name: "toaster", Kind: "Deployment"}, {Name: "toaster-db", Kind: "StatefulSet"}},
			WorkloadKinds: []string{"Deployment", "StatefulSet"}, Version: "2.1.0", Replicas: "2/2", Status: StatusHealthy,
			Hosts:    []string{"toast.example.com"},
			Exposure: ExposureResult{Declared: ExposureInternet, Gateway: "front-door"},
		}, {
			Key: AppKey{Namespace: "garage", Identity: "workbench"}, FriendlyName: "Workbench", Category: Uncategorized,
			Components:    []ComponentRef{{Name: "workbench", Kind: "Deployment"}},
			WorkloadKinds: []string{"Deployment"}, Replicas: "0/1", Status: StatusDown,
		}},
		Platform: []AppRecord{{
			Key: AppKey{Namespace: "pipes", Identity: "boiler"}, FriendlyName: "Boiler", Category: "Plumbing",
			Components:    []ComponentRef{{Name: "boiler", Kind: "DaemonSet"}},
			WorkloadKinds: []string{"DaemonSet"}, Version: "1.0", Replicas: "3/3", Status: StatusHealthy,
		}},
		Unresolved: []AppRecord{{
			Key: AppKey{Namespace: "garage", Identity: "shelf"}, FriendlyName: "Shelf", Category: Uncategorized,
			Components:    []ComponentRef{{Name: "shelf", Kind: "StatefulSet"}},
			WorkloadKinds: []string{"StatefulSet"}, Replicas: "1/1", Status: StatusHealthy,
		}},
		Graveyard: []GraveyardEntry{{Name: "kettle", Namespace: "pantry", Reason: "missing since 2026-03-30"}},
	})

	golden := filepath.Join("testdata", "overview.golden.md")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden missing (run go test -update): %v", err)
	}
	if out != string(want) {
		t.Errorf("overview differs from %s; review the change and regenerate with go test -update\n got: %q\nwant: %q", golden, out, string(want))
	}
}
