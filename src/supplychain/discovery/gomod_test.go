package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestGoVersionSatisfies(t *testing.T) {
	cases := []struct {
		have, need string
		want       bool
	}{
		{"1.26.10", "1.27.0", false}, // the gateway-api case: a newer-Go dep is not satisfiable
		{"1.27.0", "1.27.0", true},
		{"1.27.1", "1.27.0", true},
		{"1.27", "1.27.0", true},  // missing patch counts as zero
		{"1.26.10", "1.26.9", true},
		{"1.26.9", "1.26.10", false},
		{"go1.27", "1.26", true}, // tolerate a go-prefix
		{"1.26.10", "1.27", false},
	}
	for _, c := range cases {
		if got := goVersionSatisfies(c.have, c.need); got != c.want {
			t.Errorf("goVersionSatisfies(%q, %q) = %v, want %v", c.have, c.need, got, c.want)
		}
	}
}

// TestGoModHoldsIncompatibleGoVersion: when the newest release declares a newer `go` directive
// than the project builds with, the resolver holds it for review (Latest stays as awareness) and
// retargets eligibility to the newest release the project CAN build — never proposing a bump that
// `go get` would refuse. This is the gateway-api v1.7.0 (needs go 1.27) blocker, generalized.
func TestGoModHoldsIncompatibleGoVersion(t *testing.T) {
	// go.mod a module version serves for its @v/<v>.mod, carrying the go directive we gate on.
	goDirective := map[string]string{"v1.6.3": "1.24.0", "v1.6.4": "1.24.0", "v1.7.0": "1.27.0"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/@latest"):
			fmt.Fprint(w, `{"Version":"v1.7.0","Time":"2026-10-01T00:00:00Z"}`)
		case strings.HasSuffix(r.URL.Path, "/@v/list"):
			fmt.Fprint(w, "v1.6.3\nv1.6.4\nv1.7.0\n")
		case strings.HasSuffix(r.URL.Path, ".mod"):
			v := strings.TrimSuffix(filepath.Base(r.URL.Path), ".mod")
			fmt.Fprintf(w, "module sigs.k8s.io/gateway-api\n\ngo %s\n", goDirective[v])
		}
	}))
	defer srv.Close()

	m := cooldownResolver(t, "go", srv.URL)
	dep := supplychain.Dependency{Name: "sigs.k8s.io/gateway-api", Ecosystem: supplychain.EcosystemGoMod, Current: "v1.6.3"}
	m.resolveGoModule(context.Background(), &dep, "1.26.10")

	if dep.Latest != "v1.7.0" {
		t.Errorf("Latest = %q, want v1.7.0 (awareness of the newest release)", dep.Latest)
	}
	if dep.LatestEligible != "v1.6.4" {
		t.Errorf("LatestEligible = %q, want v1.6.4 (newest release buildable with go 1.26.10)", dep.LatestEligible)
	}
	if dep.UpdateTarget() != "v1.6.4" {
		t.Errorf("UpdateTarget = %q, want v1.6.4 — v1.7.0 is held, not applied", dep.UpdateTarget())
	}
	if dep.Advisory == "" {
		t.Error("expected an advisory explaining the hold")
	}
}

// TestGoModCompatibleGoVersionNotHeld: when the newest release's go directive IS satisfiable,
// the resolver leaves eligibility alone (no spurious hold).
func TestGoModCompatibleGoVersionNotHeld(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/@latest"):
			fmt.Fprint(w, `{"Version":"v1.6.4","Time":"2026-10-01T00:00:00Z"}`)
		case strings.HasSuffix(r.URL.Path, ".mod"):
			fmt.Fprint(w, "module sigs.k8s.io/gateway-api\n\ngo 1.24.0\n")
		}
	}))
	defer srv.Close()

	m := cooldownResolver(t, "go", srv.URL)
	dep := supplychain.Dependency{Name: "sigs.k8s.io/gateway-api", Ecosystem: supplychain.EcosystemGoMod, Current: "v1.6.3"}
	m.resolveGoModule(context.Background(), &dep, "1.26.10")

	if dep.Latest != "v1.6.4" || dep.LatestEligible != "" {
		t.Errorf("Latest=%q LatestEligible=%q, want v1.6.4 / \"\" (no hold)", dep.Latest, dep.LatestEligible)
	}
}

func TestEscapeModPath(t *testing.T) {
	// The Go module proxy case-encodes uppercase letters as "!"+lowercase; without it
	// these modules 404 and get silently reported "unresolved".
	cases := map[string]string{
		"github.com/Masterminds/semver/v3":   "github.com/!masterminds/semver/v3",
		"github.com/BobuSumisu/aho-corasick": "github.com/!bobu!sumisu/aho-corasick",
		"github.com/Azure/azure-sdk-for-go":  "github.com/!azure/azure-sdk-for-go",
		"dario.cat/mergo":                    "dario.cat/mergo",
		"github.com/go-git/go-git/v5":        "github.com/go-git/go-git/v5",
		"golang.org/x/mod":                   "golang.org/x/mod",
	}
	for in, want := range cases {
		if got := escapeModPath(in); got != want {
			t.Errorf("escapeModPath(%q) = %q, want %q", in, got, want)
		}
	}
}
