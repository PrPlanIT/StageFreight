package discovery

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// selectImageVersions must split the two model targets correctly:
//   - Latest         = TRUE newest in the family (awareness; drives MajorAvailable)
//   - LatestEligible = newest on the SAME version line AND exact variant suffix
//
// The Dependency methods (UpdateTarget / MajorAvailable) are checked too, since
// resolveImage feeds these fields straight into the model.
func TestSelectImageVersions(t *testing.T) {
	cases := []struct {
		name       string
		current    string
		tags       []string
		wantLatest string
		wantElig   string
		wantMajor  bool
		wantTarget string
	}{
		{
			// True newest (8.5.7) is awareness; in-line eligible stays 8.3.x fpm-alpine.
			name:       "line-and-variant",
			current:    "8.3-fpm-alpine",
			tags:       []string{"8.3-fpm-alpine", "8.4-fpm-alpine", "8.5.7-fpm-alpine3.23", "8.3.15-fpm-alpine"},
			wantLatest: "8.5.7-fpm-alpine3.23",
			wantElig:   "8.3.15-fpm-alpine",
			wantMajor:  true,
			wantTarget: "8.3.15-fpm-alpine",
		},
		{
			// A numerically-higher patch in a DIFFERENT variant must not win eligibility.
			name:       "variant-under-higher-out-variant-patch",
			current:    "8.3-fpm-alpine",
			tags:       []string{"8.3.15-fpm-alpine", "8.3.20-fpm-alpine3.23"},
			wantLatest: "8.3.20-fpm-alpine3.23",
			wantElig:   "8.3.15-fpm-alpine",
			wantMajor:  true,
			wantTarget: "8.3.15-fpm-alpine",
		},
		{
			name:       "major-only",
			current:    "8",
			tags:       []string{"8", "8.3", "8.4", "9.0"},
			wantLatest: "9.0",
			wantElig:   "8.4",
			wantMajor:  true,
			wantTarget: "8.4",
		},
		{
			name:       "full-version-pin",
			current:    "3.14.3-alpine3.23",
			tags:       []string{"3.14.3-alpine3.23", "3.14.5-alpine3.23", "3.15.0-alpine3.23"},
			wantLatest: "3.15.0-alpine3.23",
			wantElig:   "3.14.5-alpine3.23",
			wantMajor:  true,
			wantTarget: "3.14.5-alpine3.23",
		},
		{
			// No newer line exists: Latest == LatestEligible, no major awareness.
			name:       "no-newer-line",
			current:    "8.5-fpm-alpine",
			tags:       []string{"8.5-fpm-alpine", "8.5.7-fpm-alpine"},
			wantLatest: "8.5.7-fpm-alpine",
			wantElig:   "8.5.7-fpm-alpine",
			wantMajor:  false,
			wantTarget: "8.5.7-fpm-alpine",
		},
		{
			// Already on the newest: eligible == current → no bump.
			name:       "already-latest",
			current:    "8.3.15-fpm-alpine",
			tags:       []string{"8.3.15-fpm-alpine"},
			wantLatest: "8.3.15-fpm-alpine",
			wantElig:   "8.3.15-fpm-alpine",
			wantMajor:  false,
			wantTarget: "8.3.15-fpm-alpine",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := version.DecomposeTag(tc.current)
			latest, eligible := selectImageVersions(current, tc.tags)
			if latest != tc.wantLatest {
				t.Errorf("Latest = %q, want %q", latest, tc.wantLatest)
			}
			// Mirror resolveImage: an empty eligible pins to the current tag.
			if eligible == "" {
				eligible = tc.current
			}
			if eligible != tc.wantElig {
				t.Errorf("LatestEligible = %q, want %q", eligible, tc.wantElig)
			}
			dep := supplychain.Dependency{Current: tc.current, Latest: latest, LatestEligible: eligible}
			if dep.MajorAvailable() != tc.wantMajor {
				t.Errorf("MajorAvailable = %v, want %v", dep.MajorAvailable(), tc.wantMajor)
			}
			if got := dep.UpdateTarget(); got != tc.wantTarget {
				t.Errorf("UpdateTarget = %q, want %q", got, tc.wantTarget)
			}
		})
	}
}

// Mirrors the REAL Docker Hub php tag shape (verified e2e): the 8.3 line carries
// a concrete bare-variant patch "8.3.31-fpm-alpine" AND patches that move the
// alpine version into the suffix ("8.3.25-fpm-alpine3.21"). Eligibility must land
// on the highest SAME-line SAME-variant tag (8.3.31-fpm-alpine, a patch) and must
// NOT cross to the numerically-higher 8.5.7-fpm-alpine3.23 (different minor AND
// variant). Latest still reports the family-wide newest for awareness.
func TestSelectImageVersions_RealisticPhpTags(t *testing.T) {
	tags := []string{
		"8.3-fpm-alpine",        // moving tag (current)
		"8.3.31-fpm-alpine",     // concrete bare-variant patch on the 8.3 line
		"8.3.25-fpm-alpine3.21", // patch in a DIFFERENT variant
		"8.3.25-fpm-alpine3.20",
		"8.3-fpm-alpine3.21",
		"8.4-fpm-alpine",
		"8.4.14-fpm-alpine3.21",
		"8.5-fpm-alpine",
		"8.5.7-fpm-alpine3.23", // family-wide newest, out of line + out of variant
		"8.5.7-fpm-alpine3.22",
		"8",
		"8.3",
		"8.3-fpm",
		"latest",
	}
	current := version.DecomposeTag("8.3-fpm-alpine")
	latest, eligible := selectImageVersions(current, tags)

	if latest != "8.5.7-fpm-alpine3.23" {
		t.Errorf("Latest = %q, want 8.5.7-fpm-alpine3.23 (family-wide awareness)", latest)
	}
	if eligible != "8.3.31-fpm-alpine" {
		t.Errorf("LatestEligible = %q, want 8.3.31-fpm-alpine (highest in-line fpm-alpine patch, not 8.5.7)", eligible)
	}

	// Model behavior: in-line PATCH bump to 8.3.31, with the out-of-line major flagged.
	dep := supplychain.Dependency{Current: "8.3-fpm-alpine", Latest: latest, LatestEligible: eligible}
	if dep.UpdateTarget() != "8.3.31-fpm-alpine" {
		t.Errorf("UpdateTarget = %q, want 8.3.31-fpm-alpine", dep.UpdateTarget())
	}
	if !dep.MajorAvailable() {
		t.Error("MajorAvailable = false, want true (8.5.7 exists out of line)")
	}
}
