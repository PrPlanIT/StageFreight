package severity

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"critical":  Critical,
		"HIGH":      High,
		" medium ":  Medium,
		"moderate":  Medium, // OSV vocab folds to MEDIUM
		"MODERATE":  Medium,
		"low":       Low,
		"":          Unknown,
		"negligible": Unknown, // unrecognized → UNKNOWN
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRank(t *testing.T) {
	if Rank("CRITICAL") <= Rank("high") || Rank("high") <= Rank("medium") || Rank("medium") <= Rank("low") {
		t.Error("rank must strictly decrease critical > high > medium > low")
	}
	if Rank("moderate") != Rank("MEDIUM") {
		t.Error("moderate must rank equal to medium")
	}
	if Rank("") != 0 || Rank("nonsense") != 0 {
		t.Error("unknown/unrecognized must rank 0")
	}
}

func TestOrder(t *testing.T) {
	// Order is the ascending sort key: most severe first, unknown last.
	want := map[string]int{"CRITICAL": 0, "HIGH": 1, "MEDIUM": 2, "MODERATE": 2, "LOW": 3, "": 4, "bogus": 4}
	for in, w := range want {
		if got := Order(in); got != w {
			t.Errorf("Order(%q) = %d, want %d", in, got, w)
		}
	}
}
