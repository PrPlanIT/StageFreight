package dependency

import (
	"context"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
)

func TestMaxFixedVersion(t *testing.T) {
	dep := freshness.Dependency{
		Ecosystem: freshness.EcosystemGoMod,
		Vulnerabilities: []freshness.VulnInfo{
			{ID: "a", FixedIn: "0.53.0"},
			{ID: "b", FixedIn: "0.55.0"}, // highest — the version that clears ALL advisories
			{ID: "c", FixedIn: ""},       // no known fix → ignored
		},
	}
	if got := maxFixedVersion(dep); got != "0.55.0" {
		t.Fatalf("maxFixedVersion = %q, want 0.55.0", got)
	}
	if got := maxFixedVersion(freshness.Dependency{}); got != "" {
		t.Fatalf("maxFixedVersion(no advisories) = %q, want empty", got)
	}
}

func TestGoVersionQuery(t *testing.T) {
	cases := map[string]string{
		"0.55.0":                        "v0.55.0", // OSV form → module form (go get needs the v)
		"v0.55.0":                       "v0.55.0", // already prefixed
		"latest":                        "latest",  // query keyword passes through
		"":                              "",
		"v1.2.3-0.20240101000000-abcde": "v1.2.3-0.20240101000000-abcde", // pseudo-version
	}
	for in, want := range cases {
		if got := goVersionQuery(in); got != want {
			t.Errorf("goVersionQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchDirectModule(t *testing.T) {
	direct := map[string]bool{
		"k8s.io/client-go":       true,
		"k8s.io/client-go/tools": true, // nested module — the LONGEST prefix must win
		"github.com/spf13/cobra": true,
	}
	cases := map[string]string{
		"k8s.io/client-go/kubernetes":  "k8s.io/client-go",
		"k8s.io/client-go/tools/cache": "k8s.io/client-go/tools",
		"golang.org/x/net/http2":       "",
		"github.com/spf13/cobra":       "github.com/spf13/cobra",
	}
	for pkg, want := range cases {
		if got := matchDirectModule(pkg, direct); got != want {
			t.Errorf("matchDirectModule(%q) = %q, want %q", pkg, got, want)
		}
	}
}

func TestResponsibleParentModule(t *testing.T) {
	// `go mod why -m` chain: main-module package → the direct dep's package → the target.
	why := "# golang.org/x/net\n" +
		"github.com/acme/app/internal/k8s\n" +
		"k8s.io/client-go/kubernetes\n" +
		"golang.org/x/net/http2\n"
	gc := goModCtx{runGo: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(why), nil
	}}
	direct := map[string]bool{"k8s.io/client-go": true, "github.com/spf13/cobra": true}
	if got := responsibleParentModule(context.Background(), gc, "golang.org/x/net", direct); got != "k8s.io/client-go" {
		t.Fatalf("responsibleParentModule = %q, want k8s.io/client-go", got)
	}

	// A module needed only by the main module has no direct parent to bump.
	whyMainOnly := "# example.com/self\nexample.com/self/pkg\n"
	gc2 := goModCtx{runGo: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(whyMainOnly), nil
	}}
	if got := responsibleParentModule(context.Background(), gc2, "example.com/self", direct); got != "" {
		t.Fatalf("responsibleParentModule(main-only) = %q, want empty", got)
	}
}

func TestApplyIgnores(t *testing.T) {
	now := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)
	deps := []freshness.Dependency{
		{Name: "x/net", Vulnerabilities: []freshness.VulnInfo{{ID: "GHSA-aaa"}, {ID: "GHSA-bbb"}}},
		{Name: "x/text", Vulnerabilities: []freshness.VulnInfo{{ID: "GHSA-ccc"}}},
	}
	ignores := []VulnIgnore{
		{ID: "ghsa-aaa"},                      // case-insensitive, no expiry → suppress
		{ID: "GHSA-bbb", Until: "2026-01-01"}, // lapsed (before now) → re-surfaces
		{ID: "GHSA-ccc", Until: "2026-12-01"}, // active (after now) → suppress
	}
	out := ApplyIgnores(deps, ignores, now)

	if ids := vulnIDList(out[0]); len(ids) != 1 || ids[0] != "GHSA-bbb" {
		t.Fatalf("x/net advisories = %v, want [GHSA-bbb] (aaa suppressed; bbb lapsed re-surfaces)", ids)
	}
	if ids := vulnIDList(out[1]); len(ids) != 0 {
		t.Fatalf("x/text advisories = %v, want none (ccc actively ignored)", ids)
	}
	if len(deps[0].Vulnerabilities) != 2 {
		t.Fatalf("ApplyIgnores mutated its input: %v", deps[0].Vulnerabilities)
	}

	// A malformed `until` is treated as expired — never silently drop a real finding.
	bad := ApplyIgnores(
		[]freshness.Dependency{{Vulnerabilities: []freshness.VulnInfo{{ID: "GHSA-xyz"}}}},
		[]VulnIgnore{{ID: "GHSA-xyz", Until: "not-a-date"}}, now)
	if len(bad[0].Vulnerabilities) != 1 {
		t.Fatalf("malformed until should not suppress; got %v", bad[0].Vulnerabilities)
	}
}

// TestParseGoGetConflict pins the batch-pin conflict resolver: a "requires Y@vN" error
// must yield (Y, vN) so the batch can raise Y — the fix for the downgrade cascade where
// x/net@0.55 requires x/sys@0.45 but x/sys was pinned to its own 0.44.
func TestParseGoGetConflict(t *testing.T) {
	cases := []struct{ name, out, wantMod, wantVer string }{
		{
			"x/net requires x/sys higher",
			"go: golang.org/x/net@v0.55.0 requires golang.org/x/sys@v0.45.0, not golang.org/x/sys@v0.44.0",
			"golang.org/x/sys", "v0.45.0",
		},
		{
			"pseudo-version required",
			"go: example.com/a@v1.2.0 requires example.com/b@v0.0.0-20240101000000-abcdef, not example.com/b@v0.0.0-000000000000-old",
			"example.com/b", "v0.0.0-20240101000000-abcdef",
		},
		{"no conflict (download line)", "go: downloading golang.org/x/net v0.55.0", "", ""},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mod, ver := parseGoGetConflict([]byte(c.out))
			if mod != c.wantMod || ver != c.wantVer {
				t.Fatalf("parseGoGetConflict = (%q, %q), want (%q, %q)", mod, ver, c.wantMod, c.wantVer)
			}
		})
	}
}

func vulnIDList(d freshness.Dependency) []string {
	ids := make([]string, 0, len(d.Vulnerabilities))
	for _, v := range d.Vulnerabilities {
		ids = append(ids, v.ID)
	}
	return ids
}
