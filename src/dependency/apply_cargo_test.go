package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// buildCargoReplacement swaps the pinned version without touching the crate name; the
// cargo update / lock sync is exercised in CI (needs a real toolchain).
func TestBuildCargoReplacement(t *testing.T) {
	cases := []struct {
		name, line, cur, latest, want, skip string
	}{
		{"simple", `serde = "1.0.150"`, "1.0.150", "1.0.200", `serde = "1.0.200"`, ""},
		{"table form", `tokio = { version = "1.0.150", features = ["full"] }`, "1.0.150", "1.0.200", `tokio = { version = "1.0.200", features = ["full"] }`, ""},
		{"caret keeps operator outside the version token", `anyhow = "^1.0.50"`, "1.0.50", "1.0.86", `anyhow = "^1.0.86"`, ""},
		{"crate name not mistaken for version", `foo0 = "0.1.0"`, "0.1.0", "0.2.0", `foo0 = "0.2.0"`, ""},
		{"current absent → skip", `serde = "1.0.150"`, "9.9.9", "1.0.200", "", "current version not found in line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, skip := buildCargoReplacement(supplychain.Dependency{Current: c.cur, Latest: c.latest}, c.line)
			if c.skip != "" {
				if skip != c.skip {
					t.Fatalf("skip = %q, want %q", skip, c.skip)
				}
				return
			}
			if skip != "" {
				t.Fatalf("unexpected skip: %q", skip)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Cargo must be in the auto-updatable set now (the whole point of the slice).
func TestCargoIsAutoUpdatable(t *testing.T) {
	if !autoUpdatableEcosystems[supplychain.EcosystemCargo] {
		t.Error("cargo must be auto-updatable")
	}
}

// groupByEcosystem routes cargo deps to the cargo bucket (not dropped).
func TestGroupByEcosystem_Cargo(t *testing.T) {
	_, _, _, cargo, _ := groupByEcosystem([]supplychain.Dependency{
		{Name: "serde", Ecosystem: supplychain.EcosystemCargo},
		{Name: "cobra", Ecosystem: supplychain.EcosystemGoMod},
	})
	if len(cargo) != 1 || cargo[0].Name != "serde" {
		t.Fatalf("cargo dep must route to the cargo bucket, got %+v", cargo)
	}
}
