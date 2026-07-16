package reconcile

import "testing"

// golangTags is a representative slice of published golang image tags across bare
// and variant flavors, patch and floating granularities, and multiple minor lines.
var golangTags = []string{
	"1", "1.24", "1.25", "1.26",
	"1.24.13", "1.25.0", "1.25.7", "1.26.5",
	"1.24-alpine3.20", "1.24.13-alpine3.20", "1.25-alpine3.20", "1.25.7-alpine3.20", "1.26.5-alpine3.20",
	"1.24-bookworm", "1.25.7-bookworm",
	"bookworm", "alpine", "latest",
}

func TestReconcileGoToolchain(t *testing.T) {
	obs := func(cur, floor string, tags []string) GoBuilderObservation {
		return GoBuilderObservation{
			File: "Dockerfile", Line: 2, Image: "golang",
			CurrentTag: cur, Floor: floor, AvailableTags: tags,
			FloorSource: "go.mod: go " + floor,
		}
	}

	cases := []struct {
		name    string
		obs     GoBuilderObservation
		wantTo  string // expected mutation target ("" = none)
		wantErr bool   // expected a config error
	}{
		{
			name:   "patch pin below floor -> minimal satisfying patch, bare",
			obs:    obs("1.24.13", "1.25.0", golangTags),
			wantTo: "golang:1.25.7",
		},
		{
			name:   "patch pin below floor -> variant preserved",
			obs:    obs("1.24.13-alpine3.20", "1.25.0", golangTags),
			wantTo: "golang:1.25.7-alpine3.20",
		},
		{
			name:   "overshoot prevented: floor 1.25 never jumps to 1.26",
			obs:    obs("1.24.13", "1.25.0", golangTags),
			wantTo: "golang:1.25.7", // NOT 1.26.5 even though it is published
		},
		{
			name: "patch pin already satisfies floor -> no mutation",
			obs:  obs("1.26.5", "1.25.0", golangTags),
		},
		{
			name: "exact patch equal to floor -> no mutation",
			obs:  obs("1.25.0", "1.25.0", golangTags),
		},
		{
			name: "minor pin floats and satisfies a higher patch floor -> no mutation",
			obs:  obs("1.25", "1.25.4", golangTags), // golang:1.25 floats to newest 1.25.x
		},
		{
			name: "minor pin below floor -> minor granularity preserved",
			obs:  obs("1.24", "1.25.0", golangTags),
			// operator tracks a floating minor; canonical is the floor's floating minor
			wantTo: "golang:1.25",
		},
		{
			name:    "minor pin below floor but floating tag unpublished -> config error",
			obs:     obs("1.24", "1.25.0", withoutTag(golangTags, "1.25")),
			wantErr: true, // 1.25.x patches exist, but we must not tighten 1.24 -> 1.25.7
		},
		{
			name:    "variant absent on floor's minor -> config error, no drift to another variant",
			obs:     obs("1.24.13-alpine3.18", "1.25.0", golangTags), // no 1.25*-alpine3.18 published
			wantErr: true,
		},
		{
			name: "non-versioned builder -> no action (floats, nothing to compare)",
			obs:  obs("bookworm", "1.25.0", golangTags),
		},
		{
			name: "unparseable floor -> no action",
			obs:  obs("1.24.13", "not-a-version", golangTags),
		},
		{
			name:    "below floor with empty tag list -> config error (known violation, no fix derivable)",
			obs:     obs("1.24.13", "1.25.0", nil),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := ReconcileGoToolchain([]GoBuilderObservation{tc.obs})

			switch {
			case tc.wantErr:
				if len(res.ConfigErrors) != 1 {
					t.Fatalf("want 1 config error, got %d (mutations=%d)", len(res.ConfigErrors), len(res.Mutations))
				}
				if len(res.Mutations) != 0 {
					t.Fatalf("want no mutation alongside config error, got %+v", res.Mutations)
				}
			case tc.wantTo != "":
				if len(res.Mutations) != 1 {
					t.Fatalf("want 1 mutation, got %d (errors=%d)", len(res.Mutations), len(res.ConfigErrors))
				}
				if got := res.Mutations[0].To; got != tc.wantTo {
					t.Fatalf("mutation To = %q, want %q", got, tc.wantTo)
				}
				if from := res.Mutations[0].From; from != "golang:"+tc.obs.CurrentTag {
					t.Fatalf("mutation From = %q, want %q", from, "golang:"+tc.obs.CurrentTag)
				}
			default:
				if len(res.Mutations) != 0 || len(res.ConfigErrors) != 0 {
					t.Fatalf("want no action, got mutations=%+v errors=%+v", res.Mutations, res.ConfigErrors)
				}
			}
		})
	}
}

// withoutTag returns a copy of tags with any exact match to drop removed.
func withoutTag(tags []string, drop string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t != drop {
			out = append(out, t)
		}
	}
	return out
}
