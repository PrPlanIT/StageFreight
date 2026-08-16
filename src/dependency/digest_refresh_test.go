package dependency

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// digestPinnedDep builds a digest-pinned Alpine base dep with the given current/resolved
// digests and no available tag bump (LatestEligible == Current), so eligibility turns
// solely on whether the pinned digest is stale.
func digestPinnedDep(pinnedDigest, resolvedDigest string) supplychain.Dependency {
	return supplychain.Dependency{
		Name:           "docker.io/library/alpine:3.23.5@sha256:" + pinnedDigest,
		Ecosystem:      supplychain.EcosystemDockerImage,
		File:           "Dockerfile",
		Current:        "3.23.5",
		Latest:         "3.23.5",
		LatestEligible: "3.23.5", // no tag bump available
		PinnedDigest:   "sha256:" + pinnedDigest,
		ResolvedDigest: "sha256:" + resolvedDigest,
	}
}

// Mode 2: same tag, but the pinned digest is stale vs the freshly-resolved one (an
// upstream rebuild). Must become an update candidate despite Current == UpdateTarget().
func TestConstruct_DigestRefreshIsCandidate(t *testing.T) {
	cs := Construct(digestPinnedDep("old", "new"), UpdateConfig{}, nil, nil)
	if !cs.Eligible {
		t.Fatalf("stale digest-pin should be a candidate, got skip: %s / %s", cs.Category, cs.Reason)
	}
}

// Same tag, matching digests — nothing to do, stays up to date (not a candidate).
func TestConstruct_DigestUpToDateNotCandidate(t *testing.T) {
	cs := Construct(digestPinnedDep("same", "same"), UpdateConfig{}, nil, nil)
	if cs.Eligible {
		t.Fatalf("matching digest-pin must be up to date, but was eligible")
	}
}

// Mode 1 regression: a version bump on a digest-pin is still a candidate (Current != target).
func TestConstruct_DigestPinVersionBumpIsCandidate(t *testing.T) {
	d := digestPinnedDep("old", "new")
	d.LatestEligible = "3.23.6" // tag bump available
	cs := Construct(d, UpdateConfig{}, nil, nil)
	if !cs.Eligible {
		t.Fatalf("version bump on a digest-pin should be a candidate, got skip: %s / %s", cs.Category, cs.Reason)
	}
}

// Regression: a non-image dep with Current == target and no digest fields stays up to
// date — the digest-refresh exemption must not perturb ordinary eligibility.
func TestConstruct_NonImageUpToDateUnaffected(t *testing.T) {
	cs := Construct(supplychain.Dependency{
		Name: "g", Ecosystem: supplychain.EcosystemGoMod, File: "go.mod",
		Current: "1.0.0", Latest: "1.0.0",
	}, UpdateConfig{}, nil, nil)
	if cs.Eligible {
		t.Fatalf("non-image up-to-date dep must not be a candidate")
	}
}

// End-to-end at the apply layer: a stale digest-pin (same tag) rewrites only the digest.
func TestDigestRefresh_AppliesNewDigestSameTag(t *testing.T) {
	dep := digestPinnedDep("old", "new")
	got, skip := buildFromReplacement(dep, "FROM docker.io/library/alpine:3.23.5@sha256:old")
	if skip != "" {
		t.Fatalf("unexpected skip: %q", skip)
	}
	want := "FROM docker.io/library/alpine:3.23.5@sha256:new"
	if got != want {
		t.Errorf("replacement = %q, want %q", got, want)
	}
}
