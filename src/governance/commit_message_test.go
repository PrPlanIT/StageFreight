package governance

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Subject names WHERE the policy came from; the body is the provenance trailer. The
// source ref and the list of written paths are gone — a commit already has a SHA and
// already shows its files, and both varied per reconcile, so no two messages matched
// and release notes rendered a row per reconcile instead of one collapsed entry.
func TestReconcileMessageShape(t *testing.T) {
	got := buildCommitMessage("gitlab.prplanit.com/PrPlanIT/MaintenancePolicy")
	want := "chore: governance reconcile from gitlab.prplanit.com/PrPlanIT/MaintenancePolicy\n\nGoverned-By: StageFreight\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// THE load-bearing assertion: Generated-By makes the rendered CI skip the push
// (/(?m)^Generated-By: StageFreight/ → when: never). A reconcile must BUILD, or the
// satellite never runs under the policy just distributed to it.
func TestReconcileTrailerDoesNotSuppressThePipeline(t *testing.T) {
	m := buildCommitMessage("host/org/repo")
	if config.MessageHasTrailer(m, config.GeneratedByTrailer) {
		t.Fatalf("Generated-By would make the satellite skip its pipeline:\n%s", m)
	}
	if !config.MessageHasTrailer(m, config.GovernedByTrailer) {
		t.Errorf("a reconcile must carry its own provenance trailer:\n%s", m)
	}
	// Distinct from deps, so `git log --grep` can separate a reconcile from a bump.
	if config.MessageHasTrailer(m, config.UpdatedByTrailer) {
		t.Errorf("a reconcile is not a dependency update:\n%s", m)
	}
}

// Identical across reconciles from one control repo, so they collapse — but different
// hosts stay distinguishable, since a bare path cannot say which forge governs a repo.
func TestReconcileMessageDedupesButKeepsHost(t *testing.T) {
	if buildCommitMessage("h/o/r") != buildCommitMessage("h/o/r") {
		t.Error("same source must give identical text")
	}
	if buildCommitMessage("gitlab.prplanit.com/PrPlanIT/MP") == buildCommitMessage("github.com/PrPlanIT/MP") {
		t.Error("different hosts must remain distinguishable")
	}
	if strings.Count(buildCommitMessage("h/o/r"), "\n") != 3 {
		t.Error("message must stay subject + blank + trailer")
	}
}
