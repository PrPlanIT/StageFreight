package dependency

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// buildFromReplacement must do a targeted swap of the resolved tag, preserving
// everything else on the FROM line. Given a correctly resolved on-line Latest
// (the freshness layer guarantees the version line + variant), the apply layer
// keeps the "fpm-alpine" variant intact.
func TestBuildFromReplacement_PreservesVariant(t *testing.T) {
	dep := supplychain.Dependency{
		Ecosystem: supplychain.EcosystemDockerImage,
		Current:   "8.3-fpm-alpine",
		Latest:    "8.3.15-fpm-alpine",
	}
	got, skip := buildFromReplacement(dep, "FROM php:8.3-fpm-alpine")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	if got != "FROM php:8.3.15-fpm-alpine" {
		t.Errorf("replacement = %q, want %q", got, "FROM php:8.3.15-fpm-alpine")
	}
}

// The apply layer must bump to the eligible IN-LINE target (UpdateTarget), NOT
// the true-latest awareness value. With Latest now correctly set to the
// out-of-line "8.5.7-fpm-alpine3.23", the FROM line must still go to 8.3.15.
func TestBuildFromReplacement_BumpsToEligibleNotLatest(t *testing.T) {
	dep := supplychain.Dependency{
		Ecosystem:      supplychain.EcosystemDockerImage,
		Current:        "8.3-fpm-alpine",
		Latest:         "8.5.7-fpm-alpine3.23",
		LatestEligible: "8.3.15-fpm-alpine",
	}
	got, skip := buildFromReplacement(dep, "FROM php:8.3-fpm-alpine")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	if got != "FROM php:8.3.15-fpm-alpine" {
		t.Errorf("replacement = %q, want %q (eligible, not latest 8.5.7)", got, "FROM php:8.3.15-fpm-alpine")
	}
}

// When there is no compatibility model (LatestEligible empty — e.g. GitHub
// release / ENV pins), UpdateTarget falls back to Latest, so behavior is
// unchanged: the bump goes to Latest.
func TestBuildEnvReplacement_FallsBackToLatest(t *testing.T) {
	dep := supplychain.Dependency{
		Ecosystem: supplychain.EcosystemGitHubRelease,
		Current:   "1.2.3",
		Latest:    "1.5.0",
		Binding:   "FOO_VERSION",
	}
	got, skip := buildEnvReplacement(dep, "ENV FOO_VERSION=1.2.3")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	if got != "ENV FOO_VERSION=1.5.0" {
		t.Errorf("replacement = %q, want %q", got, "ENV FOO_VERSION=1.5.0")
	}
}

// End-to-end: a FROM line bump on the same line+variant is written back, with
// the hash guard satisfied and the variant preserved.
func TestApplyDockerfileUpdates_FromLineBump(t *testing.T) {
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	const body = "FROM php:8.3-fpm-alpine\nRUN echo hi\n"
	if err := os.WriteFile(dockerfile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	dep := supplychain.Dependency{
		Name:      "php:8.3-fpm-alpine",
		Ecosystem: supplychain.EcosystemDockerImage,
		Current:   "8.3-fpm-alpine",
		Latest:    "8.3.15-fpm-alpine",
		File:      "Dockerfile",
		Line:      1,
	}

	applied, skipped, touched, err := applyDockerfileUpdates([]supplychain.Dependency{dep}, dir)
	if err != nil {
		t.Fatalf("applyDockerfileUpdates: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", skipped)
	}
	if len(applied) != 1 {
		t.Fatalf("applied = %d, want 1", len(applied))
	}
	if len(touched) != 1 || touched[0] != "Dockerfile" {
		t.Fatalf("touched = %v, want [Dockerfile]", touched)
	}

	out, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	want := "FROM php:8.3.15-fpm-alpine\nRUN echo hi\n"
	if string(out) != want {
		t.Errorf("rewritten Dockerfile = %q, want %q", string(out), want)
	}
}
