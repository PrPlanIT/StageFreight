package prune

import "testing"

// docker/buildx filters parse durations with Go's time.ParseDuration, which has NO
// day unit — but StageFreight's own duration vocabulary does ("7d" in
// build_cache.local.retention). Passing SF's form straight through is a hard error
// ("unknown unit \"d\""), so it must be rendered into hours.
func TestDockerDuration(t *testing.T) {
	cases := map[string]string{
		"7d":   "168h", // the case that broke the live prune
		"30d":  "720h",
		"72h":  "72h",
		"90m":  "1h", // sub-hour truncates; docker filters are hour-grained
		"":     "",
		"soon": "", // unparseable → no filter, never a failed prune
	}
	for in, want := range cases {
		if got := dockerDuration(in); got != want {
			t.Errorf("dockerDuration(%q) = %q, want %q", in, got, want)
		}
	}
}

// buildx prefixes deprecation warnings before the real ERROR line; the reported
// reason must be the actionable one, not the warning.
func TestFirstLine_PrefersError(t *testing.T) {
	out := `Flag --keep-storage has been deprecated, keep-storage flag has been changed to reserved-space
ERROR: "until" filter expects a duration (e.g., '24h'): time: unknown unit "d" in duration "7d"`
	got := firstLine(out)
	if got == "" || got[:6] != "ERROR:" {
		t.Errorf("firstLine = %q, want the ERROR line", got)
	}
	if firstLine("just one line") != "just one line" {
		t.Error("a single line must pass through")
	}
}
