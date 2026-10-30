package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func cooldownResolver(t *testing.T, registry, url string) *Resolver {
	t.Helper()
	m := NewResolver()
	if err := m.Configure(map[string]any{
		"registries":      map[string]any{registry: map[string]any{"url": url}},
		"min_release_age": "3d",
	}); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return now }
	return m
}

func stamp(d time.Duration) string { return ago(d).Format(time.RFC3339) }

// TestGoModCooldown: a young @latest dates older releases newest first and stops at the
// first one past the window.
func TestGoModCooldown(t *testing.T) {
	var infos atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/@latest"):
			fmt.Fprintf(w, `{"Version":"v1.2.5","Time":%q}`, stamp(time.Hour))
		case strings.HasSuffix(r.URL.Path, "/@v/list"):
			fmt.Fprint(w, "v1.2.0\nv1.2.1\nv1.2.2\nv1.2.3\nv1.2.4\nv1.2.5\nv1.3.0-rc.1\n")
		case strings.HasSuffix(r.URL.Path, ".info"):
			infos.Add(1)
			ages := map[string]time.Duration{"v1.2.4": day, "v1.2.3": 5 * day, "v1.2.2": 9 * day, "v1.2.1": 20 * day}
			v := strings.TrimSuffix(filepath.Base(r.URL.Path), ".info")
			fmt.Fprintf(w, `{"Version":%q,"Time":%q}`, v, stamp(ages[v]))
		}
	}))
	defer srv.Close()

	m := cooldownResolver(t, "go", srv.URL)
	dep := supplychain.Dependency{Name: "example.com/mod", Ecosystem: supplychain.EcosystemGoMod, Current: "v1.2.0"}
	m.resolveGoModule(context.Background(), &dep, "")
	applyCooldown(&dep, m.cfg.MinReleaseAge, now)

	if dep.Latest != "v1.2.3" || dep.CooldownHeld != "v1.2.5" {
		t.Errorf("Latest = %s held %s, want v1.2.3 held v1.2.5", dep.Latest, dep.CooldownHeld)
	}
	if n := infos.Load(); n != 2 {
		t.Errorf("fetched %d .info files, want 2 (v1.2.4, then v1.2.3 is past the window)", n)
	}
}

// TestPyPIAndCratesDates: PyPI dates a version by its earliest non-yanked upload,
// crates.io by created_at; yanked crates are not candidates.
func TestPyPIAndCratesDates(t *testing.T) {
	pypi := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"info":{"version":"2.1.0"},"releases":{
			"2.1.0":[{"upload_time_iso_8601":%q}],
			"2.0.0":[{"upload_time_iso_8601":%q,"yanked":true},{"upload_time_iso_8601":%q}],
			"1.0.0":[{"upload_time_iso_8601":%q}]}}`, stamp(time.Hour), stamp(40*day), stamp(10*day), stamp(90*day))
	}))
	defer pypi.Close()
	m := cooldownResolver(t, "pypi", pypi.URL)
	dep := supplychain.Dependency{Name: "pkg", Ecosystem: supplychain.EcosystemPip, Current: "1.5.0"}
	m.resolvePyPI(context.Background(), &dep)
	if got := dep.Released["2.0.0"]; !got.Equal(ago(10 * day)) {
		t.Errorf("2.0.0 dated %v, want its earliest non-yanked upload", got)
	}
	if _, ok := dep.Released["1.0.0"]; ok {
		t.Error("a version older than Current must not be kept")
	}

	crates := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"crate":{"max_version":"1.3.0"},"versions":[
			{"num":"1.3.0","created_at":%q},{"num":"1.2.9","yanked":true,"created_at":%q},{"num":"1.2.5","created_at":%q}]}`,
			stamp(time.Hour), stamp(30*day), stamp(30*day))
	}))
	defer crates.Close()
	m = cooldownResolver(t, "crates", crates.URL)
	dep = supplychain.Dependency{Name: "c", Ecosystem: supplychain.EcosystemCargo, Current: "1.2.0", Constraint: "1.2.0"}
	m.resolveCrate(context.Background(), &dep)
	applyCooldown(&dep, m.cfg.MinReleaseAge, now)
	if dep.LatestEligible != "1.2.5" || dep.Latest != "1.2.5" {
		t.Errorf("got eligible %s latest %s, want 1.2.5 (1.3.0 young, 1.2.9 yanked)", dep.LatestEligible, dep.Latest)
	}
}

// TestToolchainExactPin_ListOnlyWhenYoung: an exact pin reads /releases/latest and
// fetches the release list only when that release is inside the cooldown.
func TestToolchainExactPin_ListOnlyWhenYoung(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		name       string
		latestAge  time.Duration
		wantLists  int32
		wantLatest string
	}{
		{"old latest: one request", 10 * day, 0, "0.77.0"},
		{"young latest: steps back", time.Hour, 1, "0.76.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lists atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/releases/latest") {
					fmt.Fprintf(w, `{"tag_name":"v0.77.0","published_at":%q}`, stamp(tc.latestAge))
					return
				}
				lists.Add(1)
				fmt.Fprintf(w, `[{"tag_name":"v0.77.0","published_at":%q},{"tag_name":"v0.76.5","published_at":%q}]`, stamp(tc.latestAge), stamp(8*day))
			}))
			defer srv.Close()
			m := cooldownResolver(t, "github", srv.URL)
			m.SetToolchainDesired(map[string]config.ToolConstraint{"trivy": {Constraint: "0.70.0"}})
			deps := m.checkToolchains(context.Background(), m.desired)
			m.applyCooldowns(deps)
			if lists.Load() != tc.wantLists || deps[0].Latest != tc.wantLatest {
				t.Errorf("lists %d latest %s, want %d / %s", lists.Load(), deps[0].Latest, tc.wantLists, tc.wantLatest)
			}
		})
	}
}

// TestToolchains_UnreadableLockIsReported: an old-version lock marks every toolchain
// unresolved with the regenerate hint instead of reading as empty.
func TestToolchains_UnreadableLockIsReported(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(".stagefreight", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".stagefreight/toolchains.lock", []byte(`{"lockfileVersion": 1, "toolchains": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewResolver()
	m.http = newHTTPClient(1)
	deps := m.checkToolchains(context.Background(), map[string]config.ToolConstraint{"cosign": {Constraint: "3.0.6"}})
	if len(deps) != 1 || !strings.Contains(deps[0].ResolutionError, "delete it") {
		t.Errorf("want the lock error on the dep, got %+v", deps)
	}
}
