package build

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// TestDistDir_WithinManagedRoot pins the binary/archive output root inside the
// Perform→Publish boundary. Binaries, archives, and SHA256SUMS are produced in
// the perform job and consumed by the publish job, which only receives
// artifact.ManagedRoot across the CI job boundary. If a refactor moves DistDir
// back to a bare "dist" (as it was before v0.6.1's archives silently failed to
// attach), this test is what should stop it.
func TestDistDir_WithinManagedRoot(t *testing.T) {
	if !artifact.WithinManagedRoot(".", DistDir) {
		t.Fatalf("DistDir = %q must live beneath artifact.ManagedRoot %q — "+
			"binary build output that escapes it is not forwarded from perform to publish",
			DistDir, artifact.ManagedRoot)
	}
}
