package upstream

import (
	"regexp"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// The case that motivated the guard: hlhd/ansible tracks ansible-core, upstream has released
// up to 2.21.5, and an image-only fix was tagged v2.21.6.
var (
	ansibleUpstreamTags = []string{"v2.20.9", "v2.21.4", "v2.21.5", "v2.22.0b1", "v2.22.0rc1", "devel-tip"}
	ansibleSource       = Source{Kind: "git", Location: "https://github.com/ansible/ansible", Match: regexp.MustCompile(config.DefaultUpstreamMatch)}
)

func TestReleases_DefaultMatchKeepsStableSemverNewestFirst(t *testing.T) {
	got := Releases(ansibleUpstreamTags, ansibleSource.Match)
	var tags []string
	for _, r := range got {
		tags = append(tags, r.Tag)
	}
	if strings.Join(tags, ",") != "v2.21.5,v2.21.4,v2.20.9" {
		t.Fatalf("releases = %v, want stable semver newest first", tags)
	}
}

func TestReleases_VersionGroup(t *testing.T) {
	re := regexp.MustCompile(`^release-(?P<version>\d+\.\d+\.\d+)$`)
	got := Releases([]string{"release-1.2.3", "release-1.10.0", "v9.9.9"}, re)
	if len(got) != 2 || got[0].Tag != "release-1.10.0" || got[0].Version.String() != "1.10.0" {
		t.Fatalf("got %+v, want release-1.10.0 then release-1.2.3", got)
	}
}

func TestOurVersion(t *testing.T) {
	cases := map[string]string{
		"v2.21.5":    "2.21.5",
		"2.21.5":     "2.21.5",
		"v2.8.1-3":   "2.8.1", // image revision suffix names the same upstream version
		"v0.9.2-r2":  "0.9.2",
		"v2.22.0rc1": "", // a pre-release is not a release
		"latest":     "",
	}
	for tag, want := range cases {
		v, ok := OurVersion(tag)
		got := ""
		if ok {
			got = v.String()
		}
		if got != want {
			t.Errorf("OurVersion(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestEvaluate_TrackFlagsTheUnreleasedVersion(t *testing.T) {
	up := Releases(ansibleUpstreamTags, ansibleSource.Match)
	rep := Evaluate(ansibleSource, up, []string{"v2.21.4", "v2.21.5", "v2.21.6", "latest-dev"}, true)
	if strings.Join(rep.Mismatched, ",") != "v2.21.6" {
		t.Fatalf("mismatched = %v, want [v2.21.6]", rep.Mismatched)
	}
	if rep.UpdatesAvailable {
		t.Fatal("ours (v2.21.6) is not behind upstream's newest (v2.21.5); no update is available")
	}
}

func TestEvaluate_UpdatesAvailable(t *testing.T) {
	up := Releases(ansibleUpstreamTags, ansibleSource.Match)
	rep := Evaluate(ansibleSource, up, []string{"v2.21.4"}, true)
	if !rep.UpdatesAvailable || rep.UpstreamNewest.Tag != "v2.21.5" || rep.OursNewest != "v2.21.4" {
		t.Fatalf("report = %+v, want update v2.21.4 -> v2.21.5", rep)
	}
	if len(rep.Mismatched) != 0 {
		t.Fatalf("mismatched = %v, want none", rep.Mismatched)
	}
}

func TestEvaluate_OwnModeNeverMismatches(t *testing.T) {
	up := Releases(ansibleUpstreamTags, ansibleSource.Match)
	rep := Evaluate(ansibleSource, up, []string{"v1.0.0", "v1.4.2"}, false)
	if len(rep.Mismatched) != 0 {
		t.Fatalf("own lineage cannot mismatch upstream, got %v", rep.Mismatched)
	}
}

func TestEvaluate_EmptyUpstreamFlagsNothing(t *testing.T) {
	// A pattern that selects nothing is a config problem, not proof that every tag lies.
	rep := Evaluate(ansibleSource, nil, []string{"v2.21.5"}, true)
	if len(rep.Mismatched) != 0 || rep.UpdatesAvailable {
		t.Fatalf("report = %+v, want nothing flagged", rep)
	}
}

func TestReleaseVerdict(t *testing.T) {
	up := Releases(ansibleUpstreamTags, ansibleSource.Match)
	if ok, reason := ReleaseVerdict(ansibleSource, up, "v2.21.6"); ok || !strings.Contains(reason, "2.21.6") || !strings.Contains(reason, "v2.21.5") {
		t.Fatalf("v2.21.6 must be refused naming the newest upstream release; ok=%v reason=%q", ok, reason)
	}
	if ok, _ := ReleaseVerdict(ansibleSource, up, "v2.21.5"); !ok {
		t.Fatal("v2.21.5 is released upstream and must be allowed")
	}
	if ok, _ := ReleaseVerdict(ansibleSource, up, "v2.21.5-2"); !ok {
		t.Fatal("an image revision of a released version names that version and must be allowed")
	}
	if ok, _ := ReleaseVerdict(ansibleSource, up, "nightly"); ok {
		t.Fatal("a tag naming no version cannot ship under track")
	}
}
