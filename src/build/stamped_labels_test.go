package build

import (
	"slices"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Every label StageFreight stamps must be refused as version evidence (config.StampedLabels):
// a probe on a label the build itself writes would always agree with the tag. ImageLabels is the
// single stamping surface (it subsumes StandardLabels/WithDescription), so exercising it with
// every optional field populated asserts the full emitted key set is registered.
func TestStampedLabelsAreRefusedAsEvidence(t *testing.T) {
	labels := ImageLabels(ImageLabelInput{
		PlanHash: "plan", SFVersion: "1.2.3", Commit: "abc", Mode: "standard", Created: "", Description: "d",
		Title: "t", Source: "s", URL: "u", Documentation: "doc", Licenses: "MIT", Vendor: "ven", Authors: "a <a@x>",
		BaseName: "ghcr.io/x/y:1", BaseDigest: "sha256:deadbeef", Maintenance: "tracked",
	})
	for key := range labels {
		if !slices.Contains(config.StampedLabels, key) {
			t.Errorf("StageFreight stamps %s; add it to config.StampedLabels so it cannot be used as an evidence probe", key)
		}
	}
}
