package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestHasConfiguredBadges gates the narrate badges producer: a project with no badges
// reports false so the runner skips badge generation instead of failing; a project that
// declares narrate.badges reports true.
func TestHasConfiguredBadges(t *testing.T) {
	if hasConfiguredBadges(&config.Config{}) {
		t.Error("hasConfiguredBadges(empty) = true, want false (nothing to generate ⇒ skip)")
	}

	withBadges := &config.Config{
		Stencils: config.OrderedStencils{
			{ID: "build", Label: "build", Output: ".stagefreight/badges/build.svg"},
		},
	}
	if !hasConfiguredBadges(withBadges) {
		t.Error("hasConfiguredBadges(with badge stencils) = false, want true")
	}
}

// A stencil that is removed from config leaves its generated SVG behind. Nothing
// regenerates it, because nothing declares it any more, so it sits at whatever it
// last rendered — in the fleet, nine repos carried an `updated.svg` frozen at "n/a"
// long after the stencil was split into release-updated/dev-updated. Pruning is what
// makes the output directory a function of the config rather than of its history.
func TestPruneOrphanBadges(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("<svg/>"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	keep := write("release-updated.svg")
	orphan := write("updated.svg")
	notOurs := write("diagram.png")      // not an SVG we generate
	nested := filepath.Join(dir, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(nested, "elsewhere.svg") // different directory
	if err := os.WriteFile(outside, []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	pruned, err := pruneOrphanBadges([]string{keep})
	if err != nil {
		t.Fatalf("pruneOrphanBadges: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != orphan {
		t.Fatalf("pruned = %v, want exactly [%s]", pruned, orphan)
	}
	for _, p := range []string{keep, notOurs, outside} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed but should have been kept: %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan %s still present", filepath.Base(orphan))
	}
}

// A run that generated nothing must not delete everything. If badge resolution fails
// or a filtered run produces no outputs, an empty "written" set would otherwise read
// as "no badge belongs here" and clear the directory.
func TestPruneOrphanBadgesRefusesEmptyWrittenSet(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "build.svg")
	if err := os.WriteFile(p, []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	pruned, err := pruneOrphanBadges(nil)
	if err != nil {
		t.Fatalf("pruneOrphanBadges(nil): %v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("pruned %v from an empty written set; must prune nothing", pruned)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("build.svg was removed on an empty run: %v", err)
	}
}
