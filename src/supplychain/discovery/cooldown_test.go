package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

const day = 24 * time.Hour

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

func flat(d time.Duration) supplychain.Cooldown {
	return supplychain.Cooldown{Major: d, Minor: d, Patch: d}
}

func TestApplyCooldown(t *testing.T) {
	t.Run("young latest steps back to the newest aged release", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemGoMod, Current: "1.2.0", Latest: "1.2.3",
			Released:          map[string]time.Time{"1.2.1": ago(10 * day), "1.2.2": ago(4 * day), "1.2.3": ago(time.Hour)},
			AvailableVersions: []string{"1.1.0", "1.2.0", "1.2.1", "1.2.2", "1.2.3"},
		}
		applyCooldown(&dep, flat(3*day), now)
		if dep.Latest != "1.2.2" || dep.CooldownHeld != "1.2.3" {
			t.Errorf("Latest = %s held %s, want 1.2.2 held 1.2.3", dep.Latest, dep.CooldownHeld)
		}
		if slices.Contains(dep.AvailableVersions, "1.2.3") || !slices.Contains(dep.AvailableVersions, "1.1.0") {
			t.Errorf("only young newer versions leave AvailableVersions, got %v", dep.AvailableVersions)
		}
	})

	t.Run("each update size has its own window", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemGoMod, Current: "1.2.0", Latest: "1.3.0",
			Released: map[string]time.Time{"1.3.0": ago(5 * day), "1.2.5": ago(2 * day)},
		}
		applyCooldown(&dep, supplychain.Cooldown{Major: 30 * day, Minor: 7 * day, Patch: day}, now)
		if dep.Latest != "1.2.5" || dep.CooldownHeld != "1.3.0" {
			t.Errorf("a 5d minor must wait for 7d while a 2d patch clears 1d: Latest = %s held %s", dep.Latest, dep.CooldownHeld)
		}
	})

	t.Run("nothing aged holds at current", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemCargo, Current: "1.2.0", Latest: "1.2.1",
			Released: map[string]time.Time{"1.2.1": ago(time.Hour)},
		}
		applyCooldown(&dep, flat(3*day), now)
		if dep.Latest != "1.2.0" || dep.CooldownHeld != "1.2.1" {
			t.Errorf("Latest = %s held %s, want 1.2.0 held 1.2.1", dep.Latest, dep.CooldownHeld)
		}
	})

	t.Run("prereleases and undated versions are never stepped back to", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemNpm, Current: "1.2.0", Latest: "1.4.0",
			Released:          map[string]time.Time{"1.4.0": ago(time.Hour), "1.4.0-rc.1": ago(20 * day), "1.3.0": ago(20 * day)},
			AvailableVersions: []string{"1.3.5"},
		}
		applyCooldown(&dep, flat(3*day), now)
		if dep.Latest != "1.3.0" {
			t.Errorf("Latest = %s, want 1.3.0", dep.Latest)
		}
		if len(dep.AvailableVersions) != 0 {
			t.Errorf("an undated newer version counts as young, got %v", dep.AvailableVersions)
		}
	})

	t.Run("the compatible target stays in its range; latest steps back on its own", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemNpm, Constraint: "^1.2.0",
			Current: "1.2.0", LatestEligible: "1.4.0", Latest: "2.1.0",
			Released: map[string]time.Time{"1.4.0": ago(time.Hour), "1.3.0": ago(9 * day), "2.1.0": ago(time.Hour), "2.0.0": ago(9 * day)},
		}
		applyCooldown(&dep, flat(3*day), now)
		if dep.LatestEligible != "1.3.0" || dep.Latest != "2.0.0" || dep.CooldownHeld != "2.1.0" {
			t.Errorf("got eligible %s latest %s held %s, want 1.3.0 / 2.0.0 / 2.1.0", dep.LatestEligible, dep.Latest, dep.CooldownHeld)
		}
	})

	t.Run("an old enough release carrying the fix is preferred over the bypass", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemGoMod, Current: "1.2.0", Latest: "1.2.3",
			Released:        map[string]time.Time{"1.2.3": ago(time.Hour), "1.2.2": ago(9 * day)},
			Vulnerabilities: []supplychain.VulnInfo{{ID: "GHSA-1", FixedIn: "1.2.1"}},
		}
		applyCooldown(&dep, flat(3*day), now)
		if dep.Latest != "1.2.2" || dep.CooldownBypassed != "" || dep.CooldownHeld != "1.2.3" {
			t.Errorf("got latest %s bypassed %q held %q, want the aged fix 1.2.2", dep.Latest, dep.CooldownBypassed, dep.CooldownHeld)
		}
	})

	t.Run("an advisory with no fix bypasses nothing", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemGoMod, Current: "1.2.0", Latest: "1.2.1",
			Released:        map[string]time.Time{"1.2.1": ago(time.Hour)},
			Vulnerabilities: []supplychain.VulnInfo{{ID: "GHSA-1"}},
		}
		applyCooldown(&dep, flat(3*day), now)
		if dep.Latest != "1.2.0" || dep.CooldownBypassed != "" {
			t.Errorf("got latest %s bypassed %q, want the young release held", dep.Latest, dep.CooldownBypassed)
		}
	})

	t.Run("undated versions below a release past every window count as aged", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemGoMod, Current: "1.2.0", Latest: "1.2.9",
			Released:          map[string]time.Time{"1.2.9": ago(time.Hour), "1.2.6": ago(30 * day)},
			AvailableVersions: []string{"1.2.3", "1.2.6", "1.2.8", "1.2.9"},
		}
		applyCooldown(&dep, supplychain.Cooldown{Major: 14 * day, Minor: 7 * day, Patch: 3 * day}, now)
		if !slices.Equal(dep.AvailableVersions, []string{"1.2.3", "1.2.6"}) {
			t.Errorf("AvailableVersions = %v, want [1.2.3 1.2.6] (1.2.8 undated above the floor)", dep.AvailableVersions)
		}
	})

	t.Run("a young release that alone carries the fix bypasses the cooldown", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "x", Ecosystem: supplychain.EcosystemGoMod, Current: "1.2.0", Latest: "1.2.1",
			Released:        map[string]time.Time{"1.2.1": ago(time.Hour)},
			Vulnerabilities: []supplychain.VulnInfo{{ID: "GHSA-1", FixedIn: "1.2.1"}},
		}
		applyCooldown(&dep, flat(3*day), now)
		if dep.Latest != "1.2.1" || dep.CooldownHeld != "" || dep.CooldownBypassed != "1.2.1" {
			t.Errorf("got latest %s held %q bypassed %q, want 1.2.1 adopted and disclosed", dep.Latest, dep.CooldownHeld, dep.CooldownBypassed)
		}
	})

	t.Run("no release dates leaves the dependency alone", func(t *testing.T) {
		dep := supplychain.Dependency{Name: "x", Ecosystem: supplychain.EcosystemDockerImage, Current: "1.2", Latest: "1.3"}
		applyCooldown(&dep, flat(3*day), now)
		if dep.Latest != "1.3" || dep.CooldownHeld != "" {
			t.Errorf("an undated ecosystem must be untouched, got %+v", dep)
		}
	})

	t.Run("a first lock is born at the newest aged release in its line", func(t *testing.T) {
		dep := supplychain.Dependency{
			Name: "trivy", Ecosystem: supplychain.EcosystemToolchain, Constraint: "0.76.x", LockPending: true,
			Current: "0.76.2", Latest: "0.76.2",
			Released: map[string]time.Time{"0.76.2": ago(time.Hour), "0.76.1": ago(8 * day), "0.75.9": ago(time.Hour * 2)},
		}
		applyCooldown(&dep, flat(3*day), now)
		if dep.Current != "0.76.1" || dep.Latest != "0.76.1" || dep.CooldownHeld != "0.76.2" {
			t.Errorf("got current %s latest %s held %s, want lock born at 0.76.1", dep.Current, dep.Latest, dep.CooldownHeld)
		}
	})
}

// TestToolchainCooldown_EndToEnd drives checkToolchains against a fake GitHub: drafts
// and prereleases are skipped, release dates come from published_at, the per-tool
// cooldown overrides the default, and a held release is disclosed.
func TestToolchainCooldown_EndToEnd(t *testing.T) {
	t.Chdir(t.TempDir()) // no lock: trivy is LockPending
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"v0.78.0","draft":true,"published_at":"2026-10-01T00:00:00Z"},
			{"tag_name":"v0.77.0-rc1","prerelease":true,"published_at":"2026-10-01T00:00:00Z"},
			{"tag_name":"v0.76.2","published_at":"2026-10-26T06:00:00Z"},
			{"tag_name":"v0.76.1","published_at":"2026-10-10T00:00:00Z"}]`))
	}))
	defer srv.Close()

	m := NewResolver()
	if err := m.Configure(map[string]any{
		"registries":      map[string]any{"github": map[string]any{"url": srv.URL}},
		"min_release_age": "3d",
	}); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return now }
	zero := supplychain.Cooldown{}
	m.SetToolchainDesired(map[string]config.ToolConstraint{
		"trivy": {Constraint: "0.x"},
		"syft":  {Constraint: "0.x", MinReleaseAge: &zero},
	})
	deps := m.checkToolchains(context.Background(), m.desired)
	m.applyCooldowns(deps)

	got := map[string]supplychain.Dependency{}
	for _, d := range deps {
		got[d.Name] = d
	}
	if d := got["trivy"]; d.Current != "0.76.1" || d.CooldownHeld != "0.76.2" {
		t.Errorf("trivy: current %s held %s, want 0.76.1 with 0.76.2 held (draft and rc skipped)", d.Current, d.CooldownHeld)
	}
	if d := got["syft"]; d.Current != "0.76.2" || d.CooldownHeld != "" {
		t.Errorf("syft's min_release_age: 0 must adopt 0.76.2 at once, got current %s held %q", d.Current, d.CooldownHeld)
	}
}

func TestOSVIdentity(t *testing.T) {
	cases := []struct {
		dep       supplychain.Dependency
		eco, name string
	}{
		{supplychain.Dependency{Name: "cosign", Ecosystem: supplychain.EcosystemToolchain, Current: "3.0.6"}, "Go", "github.com/sigstore/cosign/v3"},
		{supplychain.Dependency{Name: "trivy", Ecosystem: supplychain.EcosystemToolchain, Current: "0.76.1"}, "Go", "github.com/aquasecurity/trivy"},
		{supplychain.Dependency{Name: "cargo-nextest", Ecosystem: supplychain.EcosystemToolchain, Current: "0.9.1"}, "crates.io", "cargo-nextest"},
		{supplychain.Dependency{Name: "kubectl", Ecosystem: supplychain.EcosystemToolchain, Current: "1.34.2"}, "", ""},
		{supplychain.Dependency{Name: "serde", Ecosystem: supplychain.EcosystemCargo, Current: "1.0.0"}, "crates.io", "serde"},
	}
	for _, tc := range cases {
		if eco, name := osvIdentity(tc.dep); eco != tc.eco || name != tc.name {
			t.Errorf("%s: got %s %s, want %s %s", tc.dep.Name, eco, name, tc.eco, tc.name)
		}
	}
}
