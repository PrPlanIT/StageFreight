package dependency

import (
	"os"
	"path/filepath"
	"strings"
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

// A digest-pinned base (image:tag@sha256:…) must bump BOTH the tag and the digest,
// swapping in the ResolvedDigest discovery resolved for the update target
// (Renovate pinDigests parity — update mode 1: version bump).
func TestBuildFromReplacement_DigestPinnedBumpsTagAndDigest(t *testing.T) {
	dep := supplychain.Dependency{
		Ecosystem:      supplychain.EcosystemDockerImage,
		Current:        "3.23.5",
		LatestEligible: "3.23.6",
		ResolvedDigest: "sha256:new",
	}
	got, skip := buildFromReplacement(dep, "FROM docker.io/library/alpine:3.23.5@sha256:old")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	want := "FROM docker.io/library/alpine:3.23.6@sha256:new"
	if got != want {
		t.Errorf("replacement = %q, want %q", got, want)
	}
}

// Same tag, new digest (an upstream CVE rebuild) — the apply layer swaps only the
// digest (update mode 2: digest refresh).
func TestBuildFromReplacement_DigestRefreshSameTag(t *testing.T) {
	dep := supplychain.Dependency{
		Ecosystem:      supplychain.EcosystemDockerImage,
		Current:        "3.23.5",
		LatestEligible: "3.23.5", // no tag bump
		ResolvedDigest: "sha256:rebuilt",
	}
	got, skip := buildFromReplacement(dep, "FROM alpine:3.23.5@sha256:old")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	want := "FROM alpine:3.23.5@sha256:rebuilt"
	if got != want {
		t.Errorf("replacement = %q, want %q", got, want)
	}
}

// Registry miss → ResolvedDigest empty: bump the tag but keep the existing digest
// rather than skipping the update entirely.
func TestBuildFromReplacement_DigestPinnedFallbackKeepsDigest(t *testing.T) {
	dep := supplychain.Dependency{
		Ecosystem:      supplychain.EcosystemDockerImage,
		Current:        "3.23.5",
		LatestEligible: "3.23.6",
	}
	got, skip := buildFromReplacement(dep, "FROM alpine:3.23.5@sha256:old")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	want := "FROM alpine:3.23.6@sha256:old"
	if got != want {
		t.Errorf("replacement = %q, want %q", got, want)
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

// An ARG/ENV-anchored base image (FROM alpine:${ALPINE_VERSION}) carries the variable
// as its Binding, so buildReplacement routes to the ENV-line updater — bumping the
// `ARG VAR=…` value, not the FROM.
func TestBuildReplacement_ArgAnchoredBumpsArgLine(t *testing.T) {
	dep := supplychain.Dependency{
		Ecosystem:      supplychain.EcosystemDockerImage,
		Name:           "alpine:3.23.5",
		Current:        "3.23.5",
		LatestEligible: "3.23.6",
		Binding:        "ALPINE_VERSION",
	}
	got, skip := buildReplacement(dep, "ARG ALPINE_VERSION=3.23.5")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	if got != "ARG ALPINE_VERSION=3.23.6" {
		t.Errorf("replacement = %q, want %q", got, "ARG ALPINE_VERSION=3.23.6")
	}
}

// End-to-end: an ARG-anchored base image bumps the ARG declaration line and leaves the
// interpolated FROM untouched.
func TestApplyDockerfileUpdates_ArgAnchoredBump(t *testing.T) {
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	const body = "ARG ALPINE_VERSION=3.23.5\nFROM alpine:${ALPINE_VERSION}\nRUN echo hi\n"
	if err := os.WriteFile(dockerfile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	dep := supplychain.Dependency{
		Name:           "alpine:3.23.5",
		Ecosystem:      supplychain.EcosystemDockerImage,
		Current:        "3.23.5",
		LatestEligible: "3.23.6",
		File:           "Dockerfile",
		Line:           1,
		Binding:        "ALPINE_VERSION",
	}

	applied, skipped, _, err := applyDockerfileUpdates([]supplychain.Dependency{dep}, dir)
	if err != nil {
		t.Fatalf("applyDockerfileUpdates: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skips: %+v", skipped)
	}
	if len(applied) != 1 {
		t.Fatalf("applied = %d, want 1", len(applied))
	}

	out, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	want := "ARG ALPINE_VERSION=3.23.6\nFROM alpine:${ALPINE_VERSION}\nRUN echo hi\n"
	if string(out) != want {
		t.Errorf("rewritten Dockerfile = %q, want %q (ARG bumped, FROM untouched)", string(out), want)
	}
}

// An inline-default base (FROM alpine:${VAR:-3.23.5}) has no ARG anchor — its version
// lives in the FROM token, so the FROM line is edited in place.
func TestBuildFromReplacement_InlineDefaultBumpsInPlace(t *testing.T) {
	dep := supplychain.Dependency{
		Ecosystem:      supplychain.EcosystemDockerImage,
		Name:           "alpine:3.23.5",
		Current:        "3.23.5",
		LatestEligible: "3.23.6",
	}
	got, skip := buildFromReplacement(dep, "FROM alpine:${ALPINE_VERSION:-3.23.5}")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	if got != "FROM alpine:${ALPINE_VERSION:-3.23.6}" {
		t.Errorf("replacement = %q, want %q", got, "FROM alpine:${ALPINE_VERSION:-3.23.6}")
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

// A pin already mangled by an earlier run must be reported as the distinct,
// unrecoverable condition it is — naming the repair — not as an ordinary mismatch a
// later run might clear. It never clears: Current and Latest permanently disagree.
func TestBuildEnvReplacementReportsCorruptPin(t *testing.T) {
	dep := supplychain.Dependency{
		Current: "kustomize/v5.8.1", Latest: "5.8.1",
		Ecosystem: supplychain.EcosystemGitHubRelease, Binding: "KUSTOMIZE_VERSION",
	}
	_, skip := buildEnvReplacement(dep, "ARG KUSTOMIZE_VERSION=vkustomize/v5.8.1")
	if !strings.HasPrefix(skip, corruptPinPrefix) {
		t.Fatalf("skip = %q, want the corrupt-pin condition", skip)
	}
	if !strings.Contains(skip, `"v5.8.1"`) {
		t.Errorf("skip must name the repair value, got %q", skip)
	}
}

// The writer is the last line of defence: a target carrying a path separator is refused
// rather than committed to a Dockerfile, because one bad write wedges the pin forever.
func TestBuildEnvReplacementRefusesMangledTarget(t *testing.T) {
	dep := supplychain.Dependency{
		Current: "5.8.0", Latest: "kustomize/v5.8.1",
		Ecosystem: supplychain.EcosystemGitHubRelease, Binding: "KUSTOMIZE_VERSION",
	}
	line := "ARG KUSTOMIZE_VERSION=v5.8.0"
	got, skip := buildEnvReplacement(dep, line)
	if !strings.HasPrefix(skip, corruptPinPrefix) {
		t.Fatalf("skip = %q, want refusal to write a mangled target", skip)
	}
	if got != line {
		t.Errorf("line must be left untouched, got %q", got)
	}
}

// A well-formed bump still applies — the guards must not block the normal path.
func TestBuildEnvReplacementStillAppliesCleanBump(t *testing.T) {
	dep := supplychain.Dependency{
		Current: "5.8.0", Latest: "5.8.1",
		Ecosystem: supplychain.EcosystemGitHubRelease, Binding: "KUSTOMIZE_VERSION",
	}
	got, skip := buildEnvReplacement(dep, "ARG KUSTOMIZE_VERSION=v5.8.0")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	if got != "ARG KUSTOMIZE_VERSION=v5.8.1" {
		t.Errorf("got %q, want the v-prefix preserved bump", got)
	}
}
