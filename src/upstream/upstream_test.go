package upstream

import (
	"regexp"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

// The case that motivated the guard: hlhd/ansible tracks ansible-core, upstream has released
// up to 2.21.5, and an image-only fix was tagged v2.21.6.
var ansibleUpstreamTags = []string{"v2.20.9", "v2.21.4", "v2.21.5", "v2.22.0b1", "v2.22.0-rc.1", "devel-tip", "v2-preview-1", "002749b50-arm64", "0032aa529f273e7ff37d92832a3b19c522c7e690"}

func sch(name string) scheme.Scheme {
	s, err := scheme.Get(name)
	if err != nil {
		panic(err)
	}
	return s
}

func mustParse(name, v string) scheme.Version {
	out, err := sch(name).Parse(v)
	if err != nil {
		panic(err)
	}
	return out
}

func defaultSel() Selection {
	return Selection{Tags: regexp.MustCompile(config.DefaultUpstreamTags), Scheme: sch(scheme.Semver)}
}

func ansible(mode string) Claim {
	return Claim{Mode: mode, Source: Source{ID: "core", Kind: config.UpstreamKindGit, Location: "https://github.com/ansible/ansible", Select: defaultSel()}}
}

func tagsOf(rs []Release) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Tag)
	}
	return strings.Join(out, ",")
}

func TestReleases_DefaultSelectionKeepsStableNewestFirstAndReportsUnparsed(t *testing.T) {
	got, unparsed := Releases(ansibleUpstreamTags, defaultSel())
	if tagsOf(got) != "v2.21.5,v2.21.4,v2.20.9" {
		t.Fatalf("releases = %s, want stable versions newest first", tagsOf(got))
	}
	// devel-tip and the commit-SHA tags are not candidates; v2-preview-1 parses as the prerelease 2.0.0-preview-1 and
	// is not selected; the PEP 440 beta is a candidate that does not parse, and must be
	// reported rather than lost.
	if strings.Join(unparsed, ",") != "v2.22.0b1" {
		t.Fatalf("unparsed = %v", unparsed)
	}
}

func TestReleases_ExcludeWinsAndPrereleasesOptIn(t *testing.T) {
	sel := defaultSel()
	sel.Exclude = []*regexp.Regexp{regexp.MustCompile(`^v2-preview`), regexp.MustCompile(`b\d+$`)}
	sel.Prereleases = true
	got, unparsed := Releases(ansibleUpstreamTags, sel)
	if tagsOf(got) != "v2.22.0-rc.1,v2.21.5,v2.21.4,v2.20.9" {
		t.Fatalf("releases = %s", tagsOf(got))
	}
	if len(unparsed) != 0 {
		t.Fatalf("excluded tags must not be reported as unparsed: %v", unparsed)
	}
}

func TestReleases_VersionGroupAndTwoPartVersions(t *testing.T) {
	sel := Selection{Tags: regexp.MustCompile(`^release-(?P<version>[\d.]+)$`), Scheme: sch(scheme.Semver)}
	got, _ := Releases([]string{"release-11.4", "release-11.10", "v9.9.9"}, sel)
	if tagsOf(got) != "release-11.10,release-11.4" || got[1].Version.String() != "11.4.0" {
		t.Fatalf("got %s", tagsOf(got))
	}
}

func TestReleases_PreciseTagBeatsFloatingAlias(t *testing.T) {
	got, _ := Releases([]string{"3.15", "3.15.0", "3.14.2", "3.14"}, defaultSel())
	if tagsOf(got) != "3.15.0,3.15,3.14.2,3.14" {
		t.Fatalf("releases = %s, want the precise tag first among equal versions", tagsOf(got))
	}
}

func TestOurVersion(t *testing.T) {
	cases := map[string]string{
		"v2.21.5":     "2.21.5",
		"2.21.5":      "2.21.5",
		"v2.8.1-3":    "2.8.1", // a revision suffix names the same upstream version
		"v0.9.2-r2":   "0.9.2",
		"v1.2.0-rc.1": "1.2.0-rc.1", // a prerelease names itself
		"v2.22.0rc1":  "",
		"latest":      "",
	}
	for tag, want := range cases {
		v, ok := OurVersion(tag, sch(scheme.Semver))
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
	up, _ := Releases(ansibleUpstreamTags, defaultSel())
	rep := Evaluate(ansible(config.UpstreamVersionTrack), up, []string{"v2.21.4", "v2.21.5", "v2.21.6", "latest-dev"})
	if strings.Join(rep.Mismatched, ",") != "v2.21.6" {
		t.Fatalf("mismatched = %v, want [v2.21.6]", rep.Mismatched)
	}
	if rep.UpdatesAvailable {
		t.Fatal("ours (v2.21.6) is not behind upstream's newest; no update is available")
	}
}

func TestEvaluate_TrackUpdatesAvailable(t *testing.T) {
	up, _ := Releases(ansibleUpstreamTags, defaultSel())
	rep := Evaluate(ansible(config.UpstreamVersionTrack), up, []string{"v2.21.4"})
	if !rep.UpdatesAvailable || rep.UpstreamNewest.Tag != "v2.21.5" || rep.OursNewest != "v2.21.4" {
		t.Fatalf("report = %+v, want update v2.21.4 -> v2.21.5", rep)
	}
}

func TestEvaluate_ContinueFlagsCollisions(t *testing.T) {
	// weave-gitops: our lineage continues a dormant upstream's numbering.
	up, _ := Releases([]string{"v0.38.0", "v0.39.0"}, defaultSel())
	rep := Evaluate(ansible(config.UpstreamVersionContinue), up, []string{"v0.39.0", "v0.39.1"})
	if strings.Join(rep.Mismatched, ",") != "v0.39.0" {
		t.Fatalf("mismatched = %v, want the collision v0.39.0 only", rep.Mismatched)
	}
	if rep.UpdatesAvailable {
		t.Fatal("continue never reports updates: upstream's numbering is ours to extend")
	}
}

func TestEvaluate_SinceIsTheCutover(t *testing.T) {
	up, _ := Releases(ansibleUpstreamTags, defaultSel())
	c := ansible(config.UpstreamVersionTrack)
	c.Since = mustParse(scheme.Semver, "2.21.0")
	rep := Evaluate(c, up, []string{"v1.0.7", "v2.21.6"})
	if strings.Join(rep.Mismatched, ",") != "v2.21.6" {
		t.Fatalf("mismatched = %v, want only the tag after the cutover", rep.Mismatched)
	}
}

func TestEvaluate_EmptyUpstreamFlagsNothing(t *testing.T) {
	// A selection that keeps nothing is a config problem, not proof that every tag lies.
	rep := Evaluate(ansible(config.UpstreamVersionTrack), nil, []string{"v2.21.5"})
	if len(rep.Mismatched) != 0 || rep.UpdatesAvailable {
		t.Fatalf("report = %+v, want nothing flagged", rep)
	}
}

func TestReleaseVerdict_Track(t *testing.T) {
	up, _ := Releases(ansibleUpstreamTags, defaultSel())
	c := ansible(config.UpstreamVersionTrack)
	if ok, reason := ReleaseVerdict(c, up, "v2.21.6"); ok || !strings.Contains(reason, "2.21.6") || !strings.Contains(reason, "v2.21.5") {
		t.Fatalf("v2.21.6 must be refused naming the newest upstream release; ok=%v reason=%q", ok, reason)
	}
	for _, tag := range []string{"v2.21.5", "v2.21.5-2"} {
		if ok, reason := ReleaseVerdict(c, up, tag); !ok {
			t.Fatalf("%s names a released version and must be allowed: %s", tag, reason)
		}
	}
	if ok, _ := ReleaseVerdict(c, up, "nightly"); ok {
		t.Fatal("a tag naming no version cannot ship under track")
	}
	if ok, reason := ReleaseVerdict(c, up, "v2.22.0-rc.1"); ok || !strings.Contains(reason, "select.prereleases") {
		t.Fatalf("an unselected prerelease must be refused with the prereleases hint; reason=%q", reason)
	}
	c.Since = mustParse(scheme.Semver, "2.22.0")
	if ok, _ := ReleaseVerdict(c, up, "v2.21.6"); !ok {
		t.Fatal("a tag before the cutover is not judged")
	}
}

func TestReleaseVerdict_Continue(t *testing.T) {
	up, _ := Releases([]string{"v0.38.0", "v0.39.0"}, defaultSel())
	c := ansible(config.UpstreamVersionContinue)
	if ok, reason := ReleaseVerdict(c, up, "v0.39.0"); ok || !strings.Contains(reason, "already released") {
		t.Fatalf("a version upstream released must be refused; ok=%v reason=%q", ok, reason)
	}
	if ok, _ := ReleaseVerdict(c, up, "v0.39.1"); !ok {
		t.Fatal("a version upstream never released is ours to take")
	}
	if ok, _ := ReleaseVerdict(c, up, "nightly"); !ok {
		t.Fatal("continue judges only tags that name a version")
	}
}

func TestReleases_PEP440ReadsWhatSemverCannot(t *testing.T) {
	sel := Selection{Tags: regexp.MustCompile(config.DefaultUpstreamTags), Scheme: sch(scheme.PEP440)}
	got, unparsed := Releases(ansibleUpstreamTags, sel)
	if tagsOf(got) != "v2.21.5,v2.21.4,v2.20.9" || len(unparsed) != 0 {
		t.Fatalf("releases = %s unparsed = %v: the beta is a prerelease, not an unreadable tag", tagsOf(got), unparsed)
	}
	sel.Prereleases = true
	got, _ = Releases(ansibleUpstreamTags, sel)
	if tagsOf(got) != "v2.22.0-rc.1,v2.22.0b1,v2.21.5,v2.21.4,v2.20.9" {
		t.Fatalf("with prereleases: %s", tagsOf(got))
	}

	c := Claim{Mode: config.UpstreamVersionTrack, Source: Source{ID: "core", Select: sel}}
	if ok, reason := ReleaseVerdict(c, got, "v2.22.0b1"); !ok {
		t.Fatalf("our tag naming upstream's beta must be allowed under pep440: %s", reason)
	}
	if ok, _ := ReleaseVerdict(c, got, "v2.21.5-2"); !ok {
		t.Fatal("our revision suffix is stripped before reading the tag with upstream's scheme")
	}
}

func TestReleases_RewriteAndLoose(t *testing.T) {
	sel := Selection{
		Tags:    regexp.MustCompile(`^(?:release_|v)(?P<version>[\d_.]+[a-z]?)$`),
		Rewrite: []Rewrite{{From: regexp.MustCompile(`_`), To: "."}},
		Scheme:  sch(scheme.Loose),
	}
	got, unparsed := Releases([]string{"release_1_6_60", "v1.6.60a", "v1.6.59", "release_1_6_61"}, sel)
	if tagsOf(got) != "release_1_6_61,v1.6.60a,release_1_6_60,v1.6.59" || len(unparsed) != 0 {
		t.Fatalf("releases = %s unparsed = %v", tagsOf(got), unparsed)
	}
	c := Claim{Mode: config.UpstreamVersionTrack, Source: Source{ID: "amg", Select: sel}}
	if ok, reason := ReleaseVerdict(c, got, "v1.6.60a"); !ok {
		t.Fatalf("a letter release must match itself: %s", reason)
	}
	if ok, _ := ReleaseVerdict(c, got, "v1.6.69"); ok {
		t.Fatal("ark-se-server's v1.6.69 names an AMG version upstream never released")
	}
}
