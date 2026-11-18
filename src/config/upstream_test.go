package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func decodeUpstream(t *testing.T, src string) (UpstreamConfig, error) {
	t.Helper()
	var holder struct {
		Upstream UpstreamConfig `yaml:"upstream"`
	}
	err := yaml.Unmarshal([]byte(src), &holder)
	return holder.Upstream, err
}

func TestUpstream_DecodesKeyedSources(t *testing.T) {
	u, err := decodeUpstream(t, `
upstream:
  sources:
    core:
      git: https://github.com/ansible/ansible
      exclude: ['^v2-preview']
    base:
      image: docker.io/library/python
  version: { track: core, since: v2.21.5 }
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Sources) != 2 || u.Sources[0].ID != "core" || u.Sources[1].ID != "base" {
		t.Fatalf("sources = %+v", u.Sources)
	}
	if kind, loc := u.Sources[1].Kind(); kind != UpstreamKindImage || loc != "docker.io/library/python" {
		t.Fatalf("kind = %s %s", kind, loc)
	}
	if mode, src := u.Version.Mode(); mode != UpstreamVersionTrack || src != "core" {
		t.Fatalf("mode = %s %s", mode, src)
	}
	if errs := validateUpstream(&Config{Upstream: u}); len(errs) != 0 {
		t.Fatalf("valid config rejected: %v", errs)
	}
	port := UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Image: "registry.local:5000/team/app"}}}
	if errs := validateUpstream(&Config{Upstream: port}); len(errs) != 0 {
		t.Fatalf("a registry port is not a tag: %v", errs)
	}
}

func TestUpstream_RetiredShapeErrorsWithTheNewOne(t *testing.T) {
	for src, want := range map[string]string{
		"upstream:\n  git:\n    - repo: https://x\n": "upstream.sources.<id>",
		"upstream:\n  version: track\n":              "version: {track: <source id>}",
	} {
		if _, err := decodeUpstream(t, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want it to name %q", src, err, want)
		}
	}
}

func TestUpstream_UnknownFieldRejected(t *testing.T) {
	if _, err := decodeUpstream(t, "upstream:\n  sources:\n    core: { git: https://x, match: v }\n"); err == nil {
		t.Fatal("an unknown source field must be rejected")
	}
}

func TestUpstream_Validation(t *testing.T) {
	cases := map[string]struct {
		u    UpstreamConfig
		want string
	}{
		"no kind":        {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a"}}}, "set exactly one kind"},
		"two kinds":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Image: "i"}}}, "found 2"},
		"image tag":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Image: "ghcr.io/x/y:1.0"}}}, "names a tag or digest"},
		"bad regex":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Select: UpstreamSelect{Exclude: []string{"("}}}}}, ".exclude[0]"},
		"unknown source": {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "b"}}, `"b" is not a declared source`},
		"two modes":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a", Continue: "a"}}, "at most one"},
		"since on own":   {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Since: "v1.0.0"}}, "only meaningful"},
		"bad since":      {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a", Since: "soon"}}, "is not a semver version"},
		"bad scheme":     {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Scheme: "calver"}}}, "unknown version scheme"},
		"bad rewrite":    {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Select: UpstreamSelect{Rewrite: []UpstreamRewrite{{From: "("}}}}}}, ".rewrite[0].from"},
		"bad track line": {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a@x"}}, "a pinned line is track:"},
	}
	for name, c := range cases {
		errs := strings.Join(validateUpstream(&Config{Upstream: c.u}), "\n")
		if !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}
}

func TestUpstream_TrackLine(t *testing.T) {
	cases := []struct {
		track      string
		wantSrc    string
		wantMajor  int
		wantPinned bool
	}{
		{"synapse", "synapse", 0, false},
		{"synapse@1", "synapse", 1, true},
		{"synapse@2", "synapse", 2, true},
		{"", "", 0, false},
	}
	for _, c := range cases {
		v := UpstreamVersion{Track: c.track}
		if _, src := v.Mode(); c.track != "" && src != c.wantSrc {
			t.Errorf("%q: Mode src=%q, want %q", c.track, src, c.wantSrc)
		}
		if major, pinned := v.TrackLine(); major != c.wantMajor || pinned != c.wantPinned {
			t.Errorf("%q: TrackLine=(%d,%v), want (%d,%v)", c.track, major, pinned, c.wantMajor, c.wantPinned)
		}
	}
	// A valid pinned line on a declared source validates clean.
	u := UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "synapse", Image: "ghcr.io/x/synapse"}}, Version: UpstreamVersion{Track: "synapse@1"}}
	if errs := validateUpstream(&Config{Upstream: u}); len(errs) != 0 {
		t.Errorf("track: synapse@1 should validate; got %v", errs)
	}
}

func TestUpstream_EvidenceValidation(t *testing.T) {
	builds := OrderedBuilds{{ID: "image", Kind: "docker"}, {ID: "cli", Kind: "binary"}}
	src := OrderedUpstreamSources{{ID: "core", Git: "https://github.com/ansible/ansible"}}
	track := UpstreamVersion{Track: "core"}
	cases := map[string]struct {
		u    UpstreamConfig
		want string // "" = valid
	}{
		"track without evidence": {UpstreamConfig{Sources: src, Version: track}, "upstream.evidence.image: version.track needs proof"},
		"track with a probe": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Package: "ansible-core", Manager: "pip"}}}}, ""},
		"track with ancestry":         {UpstreamConfig{Sources: src, Version: UpstreamVersion{Track: "core", Ancestry: true}}, ""},
		"ancestry on an image source": {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "core", Image: "ghcr.io/a/b"}}, Version: UpstreamVersion{Track: "core", Ancestry: true}}, "needs track naming a git source"},
		"evidence on a binary build": {UpstreamConfig{Sources: src, Version: UpstreamVersion{Track: "core", Ancestry: true}, Evidence: OrderedUpstreamEvidence{
			{ID: "cli", Probe: UpstreamProbe{Label: "x"}}}}, "not a docker build"},
		"two probe kinds": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Label: "x", File: "/v"}}}}, "exactly one of label | file | package"},
		"package without manager": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Package: "nut"}}}}, "package needs manager"},
		"relative file": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{File: "VERSION"}}}}, "must be an absolute path"},
		"report without reason": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Label: "v"}, OnMismatch: "report"}}}, "reason: required"},
		"version report with reason": {UpstreamConfig{Sources: src, Version: UpstreamVersion{Continue: "core", OnMismatch: "report", Reason: "shared lineage with riffcc"}}, ""},
		"stamped label": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Label: "org.opencontainers.image.version"}}}}, "proves nothing"},
		"own label": {UpstreamConfig{Sources: src, Version: track, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Label: "io.homelabhd.ansible-core.version"}}}}, ""},
		"evidence under own": {UpstreamConfig{Sources: src, Evidence: OrderedUpstreamEvidence{
			{ID: "image", Probe: UpstreamProbe{Label: "v"}}}}, "set version.track or version.continue"},
	}
	for name, c := range cases {
		errs := strings.Join(validateUpstream(&Config{Builds: builds, Upstream: c.u}), "\n")
		if (c.want == "") != (errs == "") || !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}
}

func TestUpstream_PinAndReleaseValidation(t *testing.T) {
	cases := map[string]struct {
		u    UpstreamConfig
		want string
	}{
		"two pin kinds":     {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Pins: []UpstreamPin{{File: "Dockerfile", Arg: "X", Path: "y"}}}}}, "exactly one of arg | path | marker"},
		"absolute file":     {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Pins: []UpstreamPin{{File: "/etc/x", Arg: "X"}}}}}, "repo-relative"},
		"match off marker":  {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g", Pins: []UpstreamPin{{File: "f", Arg: "X", Match: "v"}}}}}, "only used with marker"},
		"cut under own":     {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Cut: "auto"}}, "cut: only meaningful with track"},
		"unknown cut":       {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a", Ancestry: true, Cut: "nightly"}}, "auto | manual"},
		"recut when manual": {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a", Ancestry: true, Cut: "manual", Recut: "on_change"}}, "only meaningful with track and cut: auto"},
		"unknown recut":     {UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "a", Git: "g"}}, Version: UpstreamVersion{Track: "a", Ancestry: true, Recut: "always"}}, "on_change | never"},
	}
	for name, c := range cases {
		errs := strings.Join(validateUpstream(&Config{Upstream: c.u}), "\n")
		if !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}
	u := UpstreamConfig{Version: UpstreamVersion{Track: "a"}}
	if !u.AutoRelease() {
		t.Error("track releases are cut automatically by default")
	}
	if !u.AutoRecut() {
		t.Error("an auto-released line recuts on change by default")
	}
	u.Version.Recut = UpstreamRecutNever
	if u.AutoRecut() {
		t.Error("recut: never keeps version tags where they were cut")
	}
	u.Version.Recut = ""
	u.Version.Cut = UpstreamCutManual
	if u.AutoRelease() {
		t.Error("cut: manual leaves it to a human")
	}
	if u.AutoRecut() {
		t.Error("cut: manual never recuts")
	}
}

func TestUpstream_KindsLifecycleMirror(t *testing.T) {
	cases := map[string]struct {
		s    UpstreamSource
		want string // "" = valid
	}{
		"pypi":                   {UpstreamSource{ID: "a", PyPI: "ansible-core"}, ""},
		"npm":                    {UpstreamSource{ID: "a", Npm: "@scope/pkg"}, ""},
		"github release":         {UpstreamSource{ID: "a", GitHubRelease: "henrygd/beszel"}, ""},
		"github release shape":   {UpstreamSource{ID: "a", GitHubRelease: "beszel"}, "want owner/repo"},
		"apk":                    {UpstreamSource{ID: "a", Apk: "nut", Suite: "v3.22/main"}, ""},
		"apt without suite":      {UpstreamSource{ID: "a", Apt: "apt-cacher-ng"}, "needs <release>/<repository>"},
		"suite on git":           {UpstreamSource{ID: "a", Git: "g", Suite: "trixie/main"}, "only used with apk or apt"},
		"url needs tags":         {UpstreamSource{ID: "a", URL: "https://networkupstools.org/download.html"}, "required for url"},
		"url":                    {UpstreamSource{ID: "a", URL: "https://networkupstools.org/download.html", Select: UpstreamSelect{Tags: `nut-(?P<version>[\d.]+)\.tar\.gz`}}, ""},
		"mirror_of on pypi":      {UpstreamSource{ID: "a", PyPI: "x", MirrorOf: "y"}, "only git and image"},
		"dormant needs a reason": {UpstreamSource{ID: "a", Git: "g", Lifecycle: "dormant"}, "reason: required"},
		"gone with pins":         {UpstreamSource{ID: "a", Git: "g", Lifecycle: "gone", Reason: "deleted", Pins: []UpstreamPin{{File: "f", Arg: "X"}}}, "nothing to move pins to"},
		"unknown lifecycle":      {UpstreamSource{ID: "a", Git: "g", Lifecycle: "sleeping", Reason: "x"}, "unknown"},
	}
	for name, c := range cases {
		errs := strings.Join(validateUpstream(&Config{Upstream: UpstreamConfig{Sources: OrderedUpstreamSources{c.s}}}), "\n")
		if (c.want == "") != (errs == "") || !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}
	gone := UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "j", Git: "g", Lifecycle: "gone", Reason: "deleted upstream"}}, Version: UpstreamVersion{Track: "j", Ancestry: true}}
	if errs := strings.Join(validateUpstream(&Config{Upstream: gone}), "\n"); !strings.Contains(errs, "is gone") {
		t.Errorf("a gone upstream cannot be tracked: %q", errs)
	}
	if (UpstreamSource{PyPI: "x"}).EffectiveScheme() != "pep440" || (UpstreamSource{Apt: "x"}).EffectiveScheme() != "debian" || (UpstreamSource{Git: "x"}).EffectiveScheme() != "semver" {
		t.Error("the kind implies its scheme")
	}
}

func TestUpstream_SyncFileRules(t *testing.T) {
	src := OrderedUpstreamSources{{ID: "nut", Git: "https://github.com/networkupstools/nut"}}
	cases := map[string]struct {
		sync UpstreamSync
		want string // "" = valid
	}{
		"rules":         {UpstreamSync{From: "nut", Files: []UpstreamFileRule{{Match: "**", Take: "merge"}, {Match: "README.adoc", Take: "fork"}}}, ""},
		"unknown take":  {UpstreamSync{From: "nut", Files: []UpstreamFileRule{{Match: "x", Take: "ours"}}}, "merge | fork | upstream | review"},
		"empty match":   {UpstreamSync{From: "nut", Files: []UpstreamFileRule{{Take: "fork"}}}, "match: required"},
		"bad glob":      {UpstreamSync{From: "nut", Files: []UpstreamFileRule{{Match: "[", Take: "fork"}}}, "files[0].match"},
		"mirror + rule": {UpstreamSync{From: "nut", Strategy: "mirror", Branch: "master", Files: []UpstreamFileRule{{Match: "x", Take: "fork"}}}, "exact copy"},
	}
	for name, c := range cases {
		errs := strings.Join(validateUpstream(&Config{Upstream: UpstreamConfig{Sources: src, Sync: c.sync}}), "\n")
		if (c.want == "") != (errs == "") || !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}
}

func TestUpstream_RebaseRequiresForcedMirrors(t *testing.T) {
	src := OrderedUpstreamSources{{ID: "zitadel", Git: "https://github.com/zitadel/zitadel"}}
	mirror := func(force bool) OrderedRepos {
		return OrderedRepos{{ID: "github", Roles: []string{"mirror"}, Sync: SyncConfig{Branches: &FacetSpec{Scope: "all", Force: force}}}}
	}
	cfg := &Config{Repos: mirror(false), Upstream: UpstreamConfig{Sources: src, Sync: UpstreamSync{From: "zitadel"}}}
	if errs := strings.Join(validateUpstream(cfg), "\n"); !strings.Contains(errs, "repos.github.sync.branches.force: true") {
		t.Fatalf("a rebase with a keep-divergent mirror must be refused: %q", errs)
	}
	cfg.Repos = mirror(true)
	if errs := validateUpstream(cfg); len(errs) != 0 {
		t.Fatalf("a forced mirror follows the rewrite: %v", errs)
	}
	cfg.Repos = mirror(false)
	cfg.Upstream.Sync.Strategy = "merge"
	if errs := validateUpstream(cfg); len(errs) != 0 {
		t.Fatalf("a merge never rewrites, so the mirror is fine: %v", errs)
	}
}

func TestUpstreamRepositoryValidation(t *testing.T) {
	arg := []UpstreamPin{{File: "Dockerfile", Arg: "V"}}
	for name, tc := range map[string]struct {
		src  UpstreamSource
		want string
	}{
		"both":       {UpstreamSource{ID: "x", Apk: "p", Suite: "v3.24/main", Repository: "main", Pins: arg}, "suite or repository"},
		"not apk":    {UpstreamSource{ID: "x", Apt: "p", Suite: "trixie/main", Repository: "main", Pins: arg}, "only used with apk"},
		"no pins":    {UpstreamSource{ID: "x", Apk: "p", Repository: "main"}, "first pin"},
		"not an arg": {UpstreamSource{ID: "x", Apk: "p", Repository: "main", Pins: []UpstreamPin{{File: "v.yml", Path: "a"}}}, "first pin"},
		"neither":    {UpstreamSource{ID: "x", Apk: "p"}, ".suite"},
	} {
		t.Run(name, func(t *testing.T) {
			errs := validateUpstream(&Config{Upstream: UpstreamConfig{Sources: OrderedUpstreamSources{tc.src}}})
			if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Fatalf("errs=%q want %q", errs, tc.want)
			}
		})
	}
	ok := validateUpstream(&Config{Upstream: UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "x", Apk: "p", Repository: "main", Pins: arg}}}})
	if strings.Contains(strings.Join(ok, "\n"), "repository") {
		t.Fatalf("valid source refused: %q", ok)
	}
}

func TestUpstream_ForkParent(t *testing.T) {
	repos := OrderedRepos{{ID: "github-mirror", Roles: []string{"mirror"}}}
	cases := map[string]struct {
		s    UpstreamSource
		want string // "" = valid
	}{
		"fork parent":          {UpstreamSource{ID: "u", ForkParent: "github-mirror"}, ""},
		"not a repos entry":    {UpstreamSource{ID: "u", ForkParent: "gitlab"}, `"gitlab" is not a repos: entry`},
		"and a git url":        {UpstreamSource{ID: "u", ForkParent: "github-mirror", Git: "https://x"}, "set exactly one kind"},
		"mirror_of is for git": {UpstreamSource{ID: "u", ForkParent: "github-mirror", MirrorOf: "https://y"}, ""},
	}
	for name, c := range cases {
		cfg := &Config{Repos: repos, Upstream: UpstreamConfig{Sources: OrderedUpstreamSources{c.s}}}
		errs := strings.Join(validateUpstream(cfg), "\n")
		if (c.want == "") != (errs == "") || !strings.Contains(errs, c.want) {
			t.Errorf("%s: errors %q, want %q", name, errs, c.want)
		}
	}

	s := UpstreamSource{ID: "u", ForkParent: "github-mirror"}
	if k, loc := s.Kind(); k != UpstreamKindGit || loc != "" {
		t.Errorf("an unresolved fork parent is a git source with no location yet: %s %q", k, loc)
	}
	s.ForkParentGit = "https://github.com/up/r.git"
	if _, loc := s.Kind(); loc != s.ForkParentGit {
		t.Errorf("resolved, it reads the parent: %q", loc)
	}

	// A fork parent is a git source, so it can be synced from and tracked by ancestry.
	cfg := &Config{Repos: OrderedRepos{{ID: "github-mirror", Roles: []string{"mirror"}, Sync: SyncConfig{Branches: &FacetSpec{Scope: "all", Force: true}}}},
		Upstream: UpstreamConfig{Sources: OrderedUpstreamSources{{ID: "u", ForkParent: "github-mirror"}},
			Version: UpstreamVersion{Track: "u", Ancestry: true}, Sync: UpstreamSync{From: "u", Strategy: "merge"}}}
	if errs := validateUpstream(cfg); len(errs) != 0 {
		t.Errorf("track + sync from a fork parent: %v", errs)
	}
}

func TestUpstream_SyncBranch(t *testing.T) {
	for src, want := range map[string]UpstreamOnto{
		"branch: HEAD": {Head: true},
		"from: x":      {},
		"branch: beta": {Branch: "beta"},
	} {
		var sy UpstreamSync
		if err := yaml.Unmarshal([]byte(src), &sy); err != nil || sy.Onto() != want {
			t.Errorf("%s: %+v, %v", src, sy.Onto(), err)
		}
	}
	var sy UpstreamSync
	if err := yaml.Unmarshal([]byte("onto: head"), &sy); err == nil || !strings.Contains(err.Error(), "upstream.sync.onto: moved — upstream.sync.branch") {
		t.Errorf("onto names its new home: %v", err)
	}
	src := OrderedUpstreamSources{{ID: "nut", Git: "https://github.com/networkupstools/nut"}}
	mirror := UpstreamSync{From: "nut", Strategy: "mirror", Branch: "HEAD"}
	if errs := validateUpstream(&Config{Upstream: UpstreamConfig{Sources: src, Sync: mirror}}); len(errs) != 0 {
		t.Errorf("a mirror can follow upstream's default branch: %v", errs)
	}
}

// TestUpstream_MovedKeys: every retired key fails naming where it went.
func TestUpstream_MovedKeys(t *testing.T) {
	for src, want := range map[string]string{
		"upstream: {release: manual}":                                    "upstream.release: moved to upstream.version.cut",
		"upstream: {recut: never}":                                       "upstream.recut: moved to upstream.version.recut",
		"upstream: {version: {min_release_age: 3d}}":                     "upstream.version.min_release_age: moved — upstream.sources.<id>.min_release_age",
		"upstream: {sources: {a: {git: g, select: {tags: x}}}}":          "upstream.sources.<id>.select: moved",
		"upstream: {sources: {a: {github-release: o/r}}}":                "moved — github_release",
		"upstream: {evidence: {image: {probe: {file: /v}}}}":             "upstream.evidence.<build>.probe: moved",
		"upstream: {sources: {a: {git: g, tags: '^v(?P<version>.*)$'}}}": "",
	} {
		_, err := decodeUpstream(t, src)
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", src, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", src, err, want)
		}
	}
}
