package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
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
	notOurs := write("diagram.png") // not an SVG we generate
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

// {env:BUILD_STATUS} and {env:BUILD_DATE} only exist inside the pipeline. Running
// scribe apply anywhere else resolves them to nothing, and writing that would turn a
// "passing" badge into "n/a" — silently, and committed by the next docs commit. The
// unresolved value must not displace one that was resolved where it could be.
func TestUnresolvedValueDoesNotOverwriteExistingBadge(t *testing.T) {
	spec := config.BadgeSpec{Value: "{env:BUILD_STATUS}"}
	if !postbuild.ValueUnresolved(spec, "{env:BUILD_STATUS}") {
		t.Error("an unsubstituted token should read as unresolved")
	}
	if !postbuild.ValueUnresolved(spec, "") {
		t.Error("an empty value should read as unresolved")
	}
	if postbuild.ValueUnresolved(spec, "passing") {
		t.Error("a real value should not read as unresolved")
	}
	// A {{…}} literal keeps its braces on purpose — the dev-{sha} scheme names a tag
	// rather than resolving one, so it is a value, not a failure.
	lit := config.BadgeSpec{Value: "dev-{{sha}}"}
	if postbuild.ValueUnresolved(lit, "dev-{sha}") {
		t.Error("an intentional {{…}} literal must not read as unresolved")
	}
}

// A held badge is still a badge this config declares, so pruning must not treat it as
// an orphan. Holding and pruning together would otherwise delete exactly the artifact
// the hold was protecting.
func TestHeldBadgeSurvivesPruning(t *testing.T) {
	dir := t.TempDir()
	held := filepath.Join(dir, "build.svg")
	if err := os.WriteFile(held, []byte("<svg>passing</svg>"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "gone.svg")
	if err := os.WriteFile(orphan, []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// held paths are included in the written set by the generator
	pruned, err := pruneOrphanBadges([]string{held})
	if err != nil {
		t.Fatalf("pruneOrphanBadges: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != orphan {
		t.Fatalf("pruned = %v, want [%s]", pruned, orphan)
	}
	b, err := os.ReadFile(held)
	if err != nil || string(b) != "<svg>passing</svg>" {
		t.Fatalf("held badge was not preserved: %v %q", err, string(b))
	}
}
