package build

import (
	"slices"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Every label StageFreight stamps must be refused as version evidence (config.StampedLabels):
// a probe on a label the build itself writes would always agree with the tag.
func TestStampedLabelsAreRefusedAsEvidence(t *testing.T) {
	labels := StandardLabels("plan", "1.2.3", "abc", "standard", "")
	labels = WithDescription(labels, "d")
	for key := range labels {
		if !slices.Contains(config.StampedLabels, key) {
			t.Errorf("StageFreight stamps %s; add it to config.StampedLabels so it cannot be used as an evidence probe", key)
		}
	}
}
