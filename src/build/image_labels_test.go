package build

import "testing"

// ImageLabels must omit every conventional/lineage/maintenance label whose source the repo did
// not declare — StageFreight never invents a label. A minimal input yields only the core set.
func TestImageLabelsOmitsAbsentFields(t *testing.T) {
	labels := ImageLabels(ImageLabelInput{
		PlanHash: "plan", SFVersion: "1.2.3", Commit: "abc", Mode: "standard",
	})
	core := map[string]bool{
		LabelPlanHash: true, LabelVersion: true, LabelRevision: true, LabelBuildMode: true, LabelCreated: true,
	}
	for k := range labels {
		if !core[k] {
			t.Errorf("ImageLabels emitted %q from an absent source — it must be omitted, not invented", k)
		}
	}
	for _, absent := range []string{
		LabelTitle, LabelSource, LabelURL, LabelDocumentation, LabelLicenses,
		LabelVendor, LabelAuthors, LabelBaseName, LabelBaseDigest, LabelMaintenance, LabelDescription,
	} {
		if _, ok := labels[absent]; ok {
			t.Errorf("ImageLabels stamped %q with no source value", absent)
		}
	}
}

// A fully-populated input stamps every label with its trimmed value.
func TestImageLabelsSetsPresentFields(t *testing.T) {
	in := ImageLabelInput{
		PlanHash: "plan", SFVersion: "1.2.3", Commit: "abc", Mode: "standard", Created: "2026-10-31T00:00:00Z",
		Description: " desc ", Title: "Title", Source: "https://git/x/y", URL: "https://site",
		Documentation: "https://docs", Licenses: "MIT", Vendor: "homelabhd", Authors: "Me <me@x>",
		BaseName: "ghcr.io/x/y:1.2.3", BaseDigest: "sha256:deadbeef", Maintenance: "pinned",
	}
	labels := ImageLabels(in)
	want := map[string]string{
		LabelDescription:   "desc", // trimmed
		LabelTitle:         "Title",
		LabelSource:        "https://git/x/y",
		LabelURL:           "https://site",
		LabelDocumentation: "https://docs",
		LabelLicenses:      "MIT",
		LabelVendor:        "homelabhd",
		LabelAuthors:       "Me <me@x>",
		LabelBaseName:      "ghcr.io/x/y:1.2.3",
		LabelBaseDigest:    "sha256:deadbeef",
		LabelMaintenance:   "pinned",
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, labels[k], v)
		}
	}
}
