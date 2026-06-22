package disk

import "testing"

func TestSortVersionsDesc(t *testing.T) {
	v := []string{"1.24", "1.26.4", "1.24.13", "1.26.1"}
	sortVersionsDesc(v)
	want := []string{"1.26.4", "1.26.1", "1.24.13", "1.24"}
	for i := range want {
		if v[i] != want[i] {
			t.Fatalf("sortVersionsDesc = %v, want %v", v, want)
		}
	}
	// suffix tiebreak: plain before -musl
	m := []string{"1.96.0-musl", "1.96.0"}
	sortVersionsDesc(m)
	if m[0] != "1.96.0-musl" || m[1] != "1.96.0" {
		t.Errorf("musl ordering = %v", m)
	}
}

func TestToolVersionNote(t *testing.T) {
	if note, _ := toolVersionNote([]string{"0.69.3"}); note != "0.69.3" {
		t.Errorf("single = %q", note)
	}
	note, fl := toolVersionNote([]string{"1.26.4", "1.26.1", "1.24.13", "1.24"})
	if !fl.Has(FlagAttention) {
		t.Error("4 versions should flag attention")
	}
	if note != "4 versions · 1.26.4 · 1.26.1 · 1.24.13 · 1.24" {
		t.Errorf("note = %q", note)
	}
	// cap + collapse tail
	n5, fl5 := toolVersionNote([]string{"6", "5", "4", "3", "2", "1"})
	if !fl5.Has(FlagReclaimable) {
		t.Error("over-cap should flag reclaimable tail")
	}
	if n5 != "6 versions · 6 · 5 · 4 · 3 + 2 older" {
		t.Errorf("capped note = %q", n5)
	}
}

func TestImageFamilyAndProject(t *testing.T) {
	cases := []struct{ repo, family, project string }{
		{"prplanit/stagefreight", "stagefreight", "stagefreight"},
		{"ghcr.io/fork/stagefreight", "stagefreight", "stagefreight"},
		{"docker", "ci-infra", ""},
		{"gitlab/gitlab-runner", "ci-infra", ""},
		{"moby/buildkit", "ci-infra", ""},
		{"prplanit/hasteward", "other", "hasteward"},
		{"docker.cr.pcfae.com/library/rust", "other", ""}, // base image, no project
		{"alpine", "other", ""},
	}
	for _, c := range cases {
		if k, _ := imageFamily(c.repo); k != c.family {
			t.Errorf("imageFamily(%q) = %q, want %q", c.repo, k, c.family)
		}
		if p := projectOf(c.repo); p != c.project {
			t.Errorf("projectOf(%q) = %q, want %q", c.repo, p, c.project)
		}
	}
}

func TestVolumeAttr(t *testing.T) {
	a := volumeAttr("sf-dragonfly-target", "docker-host")
	if a.Project != "dragonfly" || a.Tool != "rust" {
		t.Errorf("sf-dragonfly-target → %+v, want dragonfly/rust", a)
	}
	if volumeAttr("sf-gocache-build", "docker-host").Tool != "go" {
		t.Error("sf-gocache-build → go")
	}
	if !isAnonymousVol("8d11988adf76b2bb4840ace17a750d1590beb3f8fe7cd20d3b0e61a1a212a850") {
		t.Error("64-hex is anonymous")
	}
	if isAnonymousVol("sf-gocache") {
		t.Error("named volume is not anonymous")
	}
}

func TestParseDockerSize(t *testing.T) {
	cases := map[string]int64{
		"3.1GB": 3_100_000_000, "210MB": 210_000_000, "12.1GB (66%)": 12_100_000_000,
		"1.5GiB": 1_610_612_736, "N/A": 0, "": 0,
	}
	for in, want := range cases {
		if got := parseDockerSize(in); got != want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestProjections(t *testing.T) {
	// Two scanners independently attribute to "dragonfly"; ByProject must merge.
	r := &Report{
		FS: FS{Total: 1000},
		Domains: []*Node{
			{Label: "CACHE", Kids: []*Node{
				{Label: "rust build · dragonfly", Bytes: 500, Attr: Attribution{Project: "dragonfly", Runtime: "cache-mount"}, Flags: FlagReclaimable},
			}},
			{Label: "REPOS", Kids: []*Node{
				{Label: "dragonfly", Bytes: 200, Attr: Attribution{Project: "dragonfly", Runtime: "repo-tree"}},
			}},
		},
	}
	rows := r.ByProject()
	if len(rows) != 1 || rows[0].Project != "dragonfly" || rows[0].Bytes != 700 {
		t.Fatalf("ByProject = %+v, want one dragonfly row of 700", rows)
	}
	rec := r.Reclaimable()
	if len(rec) != 1 || rec[0].Bytes != 500 {
		t.Errorf("Reclaimable = %+v, want the 500 cache node", rec)
	}
}

func TestFormatting(t *testing.T) {
	if humanBytes(1536) != "1.5 KiB" {
		t.Errorf("humanBytes = %q", humanBytes(1536))
	}
	if humanBytesShort(1610612736) != "1.5G" {
		t.Errorf("humanBytesShort = %q", humanBytesShort(1610612736))
	}
	if pctStr(5, 1000) != "<1%" || pctStr(320, 1000) != "32%" {
		t.Errorf("pctStr wrong: %q %q", pctStr(5, 1000), pctStr(320, 1000))
	}
	if got := barOf(500, 1000, 16); got != "████████········" {
		t.Errorf("barOf half = %q", got)
	}
}
