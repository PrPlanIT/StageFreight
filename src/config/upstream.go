package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

// UpstreamConfig declares the upstreams a repo repackages or forks ("upstream mode").
// StageFreight watches every declared source for new releases; version: says what our release
// tags claim about one of them, and a release whose claim is false is refused before anything
// is built.
type UpstreamConfig struct {
	// Sources are the watched upstreams, keyed by id. The id is how the rest of the config
	// refers to a source (version.track, version.continue).
	Sources OrderedUpstreamSources `yaml:"sources,omitempty"`

	// Version says what this repo's release tags claim about an upstream.
	Version UpstreamVersion `yaml:"version,omitempty"`

	// Release decides who cuts a track release when the pins name a new upstream version:
	// auto (default for track) tags the built commit after a successful default-branch run;
	// manual leaves it to a human (stagefreight tag).
	Release string `yaml:"release,omitempty"`

	// Sync keeps this repository's history on top of a git upstream: rebased or merged onto
	// its newest release (or a branch), or mirrored. Runs on the default branch.
	Sync UpstreamSync `yaml:"sync,omitempty"`

	// Evidence proves, per build, that the built artifact contains the version a release
	// claims. Keyed by build id (a docker build). Checked in publish, on the exact bytes
	// about to be distributed.
	Evidence OrderedUpstreamEvidence `yaml:"evidence,omitempty"`
}

// UpstreamEvidence is one build's proof of version.
type UpstreamEvidence struct {
	ID string `yaml:"-"`

	// Probe reads the version the built image contains.
	Probe UpstreamProbe `yaml:"probe"`

	// OnMismatch is what happens when the probe disagrees with the claimed version:
	// refuse (default) does not distribute the image; report distributes it and raises
	// version-mismatch. report needs a reason.
	OnMismatch string `yaml:"on_mismatch,omitempty"`

	// Reason says why a mismatch is tolerated (required with on_mismatch: report), e.g. an
	// upstream whose metadata trails its own tag.
	Reason string `yaml:"reason,omitempty"`
}

// UpstreamProbe reads a version out of a built image without running it. Set exactly one of
// label, file, package.
type UpstreamProbe struct {
	// Label is an OCI image label whose value is the version — one the image's own build sets
	// from what it installs (e.g. a Dockerfile LABEL fed by the pinned ARG). Labels
	// StageFreight stamps itself (org.opencontainers.image.version, …revision, …) describe the
	// build, not its contents, and are refused: a probe on them would always agree.
	Label string `yaml:"label,omitempty"`

	// File is an absolute path in the image filesystem holding the version.
	File string `yaml:"file,omitempty"`

	// Package is an installed package whose version is read from the package database
	// named by manager.
	Package string `yaml:"package,omitempty"`

	// Manager is the package database package reads: pip (site-packages dist-info),
	// dpkg (/var/lib/dpkg/status), apk (/lib/apk/db/installed).
	Manager string `yaml:"manager,omitempty"`

	// Match is a regex applied to the label value or file content; the version is its named
	// group "version", else its first group, else the whole match. Default: the first
	// dotted version.
	Match string `yaml:"match,omitempty"`
}

// OrderedUpstreamEvidence is upstream.evidence — a build id→evidence map.
type OrderedUpstreamEvidence []UpstreamEvidence

func (OrderedUpstreamEvidence) isIDMap() {}

func (o *OrderedUpstreamEvidence) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(e *UpstreamEvidence, id string) { e.ID = id })
	if err != nil {
		return fmt.Errorf("upstream.evidence: %w", err)
	}
	*o = v
	return nil
}

// Release modes.
const (
	UpstreamReleaseAuto   = "auto"
	UpstreamReleaseManual = "manual"
)

// AutoRelease reports whether StageFreight cuts track releases itself.
func (u UpstreamConfig) AutoRelease() bool {
	mode, _ := u.Version.Mode()
	return mode == UpstreamVersionTrack && u.Release != UpstreamReleaseManual
}

// Guard outcomes: what a failed version guard or probe does.
const (
	UpstreamRefuse = "refuse"
	UpstreamReport = "report"
)

// EffectiveOnMismatch returns refuse when unset.
func EffectiveOnMismatch(v string) string {
	if v == "" {
		return UpstreamRefuse
	}
	return v
}

// DefaultProbeMatch is the first dotted version in a label value or file.
const DefaultProbeMatch = `(?P<version>\d+(?:\.\d+)+[0-9A-Za-z.+\-]*)`

// StampedLabels are the OCI labels StageFreight writes on every image it builds. They state the
// build's own claims (the version being built, its commit, its time), so they can never be
// evidence of what the image contains. Kept in lockstep with build.StandardLabels by a test in
// the build package.
var StampedLabels = []string{
	"org.opencontainers.image.created",
	"org.opencontainers.image.revision",
	"org.opencontainers.image.version",
	"org.opencontainers.image.description",
	"org.stagefreight.build.mode",
	"org.stagefreight.plan.sha256",
}

// Probe package managers.
var validProbeManagers = map[string]bool{"pip": true, "dpkg": true, "apk": true}

// UpstreamSource is one watched upstream. Exactly one kind key (git, image) names it.
type UpstreamSource struct {
	ID string `yaml:"-"`

	// Git is an upstream git repository URL, e.g. https://github.com/ansible/ansible. Its
	// tags are listed anonymously (ls-remote).
	Git string `yaml:"git,omitempty"`

	// Image is an upstream container image repository without a tag, e.g.
	// ghcr.io/zitadel/zitadel. Its tags are listed anonymously (OCI registry API).
	Image string `yaml:"image,omitempty"`

	// PyPI is a Python project on pypi.org (yanked releases are not releases). Scheme pep440.
	PyPI string `yaml:"pypi,omitempty"`

	// Npm is a package on registry.npmjs.org (deprecated versions are not releases).
	Npm string `yaml:"npm,omitempty"`

	// GitHubRelease is a GitHub repository (owner/repo) whose published releases — not its
	// tags — are watched; drafts are skipped and GitHub's prerelease flag is honoured.
	GitHubRelease string `yaml:"github-release,omitempty"`

	// Apk is an Alpine package, read from the suite's APKINDEX. Needs suite. Scheme debian.
	Apk string `yaml:"apk,omitempty"`

	// Apt is a Debian package, read from the suite's Packages index. Needs suite. Scheme
	// debian.
	Apt string `yaml:"apt,omitempty"`

	// URL is a release page; every match of select.tags on it is a candidate (needs
	// select.tags), for upstreams that publish only a download page.
	URL string `yaml:"url,omitempty"`

	// Suite (apk, apt) is <release>/<repository>: v3.22/main for Alpine, trixie/main for
	// Debian.
	Suite string `yaml:"suite,omitempty"`

	// MirrorOf (git, image) is the origin this source mirrors. StageFreight lists the mirror
	// (ours, reachable) and also the origin when it answers; the mirror lagging behind its
	// origin raises mirror-behind. An unreachable origin is why mirrors exist, so it is not
	// a failure.
	MirrorOf string `yaml:"mirror_of,omitempty"`

	// Lifecycle is the upstream's state: active (default), dormant, abandoned, gone. Anything
	// but active needs a reason. A dormant or abandoned upstream that cannot be listed is not
	// reported unreachable; a gone one is not listed at all and cannot be claimed, pinned or
	// synced.
	Lifecycle string `yaml:"lifecycle,omitempty"`

	// Reason says why an upstream is not active.
	Reason string `yaml:"reason,omitempty"`

	// Select picks the upstream's releases out of everything it tags.
	Select UpstreamSelect `yaml:"select,omitempty"`

	// Scheme is how this upstream's versions are read and ordered. Default semver.
	//   semver: 1.2.3, 1.2.3-rc.1 (a missing minor/patch is zero: 11.4 = 11.4.0)
	//   pep440: Python versions — 2.22.0b1, 1.0rc1, 1.0.post1, 1!2.0
	//   loose:  1–6 dotted numbers (0.6.7.1, 2026.7.0), a trailing letter as a later release
	//           (1.6.60a > 1.6.60), a hyphen suffix as a prerelease (2.0-rc1)
	// Our release tags are read with the same scheme when a claim names this source.
	Scheme string `yaml:"scheme,omitempty"`

	// Pins are the places this upstream's version is pinned. When upstream releases, the
	// dependency update moves every pin together (or none, if they disagree), through the
	// dependency engine's commit, max_update and min_release_age.
	Pins []UpstreamPin `yaml:"pins,omitempty"`
}

// UpstreamSync is upstream.sync.
type UpstreamSync struct {
	// From names the git source to sync from.
	From string `yaml:"from,omitempty"`

	// Onto is what to sync onto: release (default — the newest release the source's
	// select: keeps) or {branch: <name>} (the head of an upstream branch).
	Onto UpstreamOnto `yaml:"onto,omitempty"`

	// Strategy is how upstream's history joins ours:
	//   rebase (default): our commits are replayed on top of upstream's, our branch is
	//                     rewritten (the forge must let StageFreight force-push it).
	//   merge:            a merge commit joins upstream into our branch; nothing is rewritten.
	//   mirror:           our branch follows upstream's exactly (fast-forward only; a diverged
	//                     branch is kept and reported, never overwritten).
	Strategy string `yaml:"strategy,omitempty"`

	// Shadow, when set, is a branch of ours kept at upstream's synced commit — a plain copy of
	// upstream to diff against.
	Shadow string `yaml:"shadow,omitempty"`

	// OnConflict is what happens when upstream's changes overlap ours:
	//   mr (default): nothing is rewritten; a merge request from upstream's commit into our
	//                 branch is opened for a human, and sync-conflict is signalled.
	//   fail:         the run fails.
	//   notify:       sync-conflict is signalled; nothing else.
	OnConflict string `yaml:"on_conflict,omitempty"`

	// Files decides, per path, what happens when upstream changes a file. Rules are read top
	// to bottom and the last one that matches wins, so broad rules go first and exceptions
	// after. A path no rule matches is merged. StageFreight's own files (.stagefreight.yml,
	// .stagefreight/, the rendered CI files) always keep the fork's version.
	Files []UpstreamFileRule `yaml:"files,omitempty"`
}

// UpstreamFileRule is one upstream.sync.files rule.
type UpstreamFileRule struct {
	// Match is a path glob (** spans directories), e.g. README.adoc, docs/**, **/*.md.
	Match string `yaml:"match"`

	// Take is what wins when upstream changes a matching file:
	//   merge (default): combine line by line; edits that overlap are a conflict.
	//   fork:            keep the fork's version (including a deletion); upstream's change is
	//                    held back, and the sync reports it.
	//   upstream:        take upstream's version; the fork's edits to it are dropped.
	//   review:          always route the change through a merge request, even when clean.
	Take string `yaml:"take"`
}

// Sync file outcomes.
const (
	TakeMerge    = "merge"
	TakeFork     = "fork"
	TakeUpstream = "upstream"
	TakeReview   = "review"
)

// UpstreamOnto is upstream.sync.onto: the scalar release, or {branch: <name>}.
type UpstreamOnto struct {
	Branch string `yaml:"branch,omitempty"`
}

// UnmarshalYAML accepts the scalar release or a {branch:} map.
func (o *UpstreamOnto) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		if n.Value != "release" {
			return fmt.Errorf("upstream.sync.onto: %q — use release or {branch: <name>}", n.Value)
		}
		*o = UpstreamOnto{}
		return nil
	}
	type plain UpstreamOnto
	var p plain
	if err := strictNodeDecode(n, &p); err != nil {
		return fmt.Errorf("upstream.sync.onto: %w", err)
	}
	*o = UpstreamOnto(p)
	return nil
}

// Sync strategies and conflict outcomes.
const (
	SyncRebase = "rebase"
	SyncMerge  = "merge"
	SyncMirror = "mirror"

	SyncConflictMR     = "mr"
	SyncConflictFail   = "fail"
	SyncConflictNotify = "notify"
)

// declared reports whether any sync field is set.
func (s UpstreamSync) declared() bool {
	return s.From != "" || s.Onto.Branch != "" || s.Strategy != "" || s.Shadow != "" || s.OnConflict != "" || len(s.Files) > 0
}

// EffectiveStrategy returns the sync strategy (rebase when unset).
func (s UpstreamSync) EffectiveStrategy() string {
	if s.Strategy == "" {
		return SyncRebase
	}
	return s.Strategy
}

// EffectiveOnConflict returns the conflict outcome (mr when unset).
func (s UpstreamSync) EffectiveOnConflict() string {
	if s.OnConflict == "" {
		return SyncConflictMR
	}
	return s.OnConflict
}

// UpstreamPin is one place a source's version is pinned. Set exactly one of arg, path,
// marker.
type UpstreamPin struct {
	// File is the repo-relative file holding the pin.
	File string `yaml:"file"`

	// Arg is a Dockerfile ARG or ENV name whose value is the version.
	Arg string `yaml:"arg,omitempty"`

	// Path is a dotted path to a scalar in a YAML or JSON file (ansible.core, apps.0.tag).
	Path string `yaml:"path,omitempty"`

	// Marker pins the line after a comment containing "stagefreight: upstream=<source id>",
	// for any other format. Match picks the version on that line.
	Marker bool `yaml:"marker,omitempty"`

	// Match (marker only) is a regex whose version group (else first group) is the pinned
	// version. Default: the first dotted version on the line.
	Match string `yaml:"match,omitempty"`
}

// UpstreamSelect picks releases from an upstream's tags: candidates (tags), minus exclusions,
// minus prereleases unless enabled. A candidate whose version does not parse is reported,
// never guessed at.
type UpstreamSelect struct {
	// Tags is a regex choosing candidate tags. The version is the named group "version", else
	// the first group, else the whole tag. Default: tags that start with a dotted version
	// (1.2, v11.4, 2.21.5), optionally v-prefixed.
	Tags string `yaml:"tags,omitempty"`

	// Exclude lists regexes; a tag matching any of them is never a release. Exclusion wins
	// over tags.
	Exclude []string `yaml:"exclude,omitempty"`

	// Prereleases admits upstream prereleases (as the scheme defines them) as releases.
	// Default false.
	Prereleases bool `yaml:"prereleases,omitempty"`

	// Rewrite edits the extracted version before it is parsed, in order: each from is a regex
	// replaced by to (with $1-style group references), e.g. {from: '_', to: '.'} reads
	// release_1_2_3 as 1.2.3.
	Rewrite []UpstreamRewrite `yaml:"rewrite,omitempty"`
}

// UpstreamRewrite is one select.rewrite step.
type UpstreamRewrite struct {
	From string `yaml:"from"`
	To   string `yaml:"to,omitempty"` // empty deletes the match
}

// UpstreamVersion is what our release tags claim. Set at most one of track, continue, own;
// none set means own.
type UpstreamVersion struct {
	// Track names the source whose version our release IS. A release naming a version that
	// source never released is refused. For an image source this also makes that upstream's
	// version the authority for the fork's base image: the base advances to upstream's
	// latest GA within the tracked major, and a newer major is held for review. The form
	// track: <source>@<major> pins a major line — the escape hatch to adopt a new major
	// (synapse@2 from a 1.x base) or to stay on one.
	Track string `yaml:"track,omitempty"`

	// Continue names the source whose numbering our own lineage continues (a dormant or
	// diverged upstream). A release naming a version that source has released is refused.
	Continue string `yaml:"continue,omitempty"`

	// Own declares our own version lineage: upstreams are watched, never enforced. The
	// default when neither track nor continue is set.
	Own *struct{} `yaml:"own,omitempty"`

	// MinReleaseAge is the FOLLOW cooldown for a tracked base image: how old an upstream GA
	// must be before this fork follows it onto the Dockerfile FROM. track makes the
	// upstream's version the fork's; this says the fork waits until that release has had
	// time to settle, aged from the release's real publish time — the tag's image creation
	// time read from its own registry — so a tag already weeks old is adopted at once, not
	// delayed from when StageFreight first saw it. A reproducible-build image that zeroes
	// that field falls back to the first-sighting clock in .stagefreight/upstream.lock. A
	// duration ("3d", "72h") or the per-size map; 0 follows the instant upstream publishes.
	// Unset inherits dependency.min_release_age. It governs ONLY the tracked base, so a fork
	// can chase or hold upstream without touching its other dependencies' cooldown.
	// Meaningful only with track.
	MinReleaseAge *supplychain.Cooldown `yaml:"min_release_age,omitempty"`

	// Since is the cutover tag: only release tags at or after this version are judged, so a
	// mode change never reinterprets older tags.
	Since string `yaml:"since,omitempty"`

	// Ancestry (track, git source) requires upstream's commit for the claimed version to be an
	// ancestor of our release commit: a history fork may reuse upstream's tag names only for
	// code that contains upstream's release. It is evidence in its own right, standing in for
	// a probe where the build stamps its version itself.
	Ancestry bool `yaml:"ancestry,omitempty"`

	// OnMismatch is what happens when a release's claim is false (track: upstream never
	// released it or ancestry fails; continue: upstream already released it): refuse
	// (default) blocks the release; report lets it ship and raises version-mismatch. report
	// needs a reason.
	OnMismatch string `yaml:"on_mismatch,omitempty"`

	// Reason says why a false claim is tolerated (required with on_mismatch: report).
	Reason string `yaml:"reason,omitempty"`
}

// OrderedUpstreamSources is upstream.sources — an id→source map (key becomes UpstreamSource.ID).
type OrderedUpstreamSources []UpstreamSource

func (OrderedUpstreamSources) isIDMap() {}

func (o *OrderedUpstreamSources) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(s *UpstreamSource, id string) { s.ID = id })
	if err != nil {
		return fmt.Errorf("upstream.sources: %w", err)
	}
	*o = v
	return nil
}

// UnmarshalYAML rejects the retired slice-1 shape with the new one as the error, then decodes
// strictly.
func (u *UpstreamConfig) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i].Value, n.Content[i+1]
			switch {
			case key == "git" || key == "images":
				return fmt.Errorf("upstream.%s: sources are now an id-keyed map: upstream.sources.<id>: {git: <url>} or {image: <repository>}, with match: moved to select.tags", key)
			case key == "version" && val.Kind == yaml.ScalarNode:
				return fmt.Errorf("upstream.version: now names the source: version: {track: <source id>} | {continue: <source id>} | {own: {}}")
			}
		}
	}
	type plain UpstreamConfig
	var p plain
	if err := strictNodeDecode(n, &p); err != nil {
		return err
	}
	*u = UpstreamConfig(p)
	return nil
}

// Upstream source kinds — the YAML key that names each.
const (
	UpstreamKindGit           = "git"
	UpstreamKindImage         = "image"
	UpstreamKindPyPI          = "pypi"
	UpstreamKindNpm           = "npm"
	UpstreamKindGitHubRelease = "github-release"
	UpstreamKindApk           = "apk"
	UpstreamKindApt           = "apt"
	UpstreamKindURL           = "url"
)

// kinds returns every kind key with its value, in a fixed order.
func (s UpstreamSource) kinds() [][2]string {
	return [][2]string{
		{UpstreamKindGit, s.Git}, {UpstreamKindImage, s.Image}, {UpstreamKindPyPI, s.PyPI},
		{UpstreamKindNpm, s.Npm}, {UpstreamKindGitHubRelease, s.GitHubRelease},
		{UpstreamKindApk, s.Apk}, {UpstreamKindApt, s.Apt}, {UpstreamKindURL, s.URL},
	}
}

// UpstreamKinds lists the kind keys, for messages and docs.
func UpstreamKinds() []string {
	var out []string
	for _, k := range (UpstreamSource{}).kinds() {
		out = append(out, k[0])
	}
	return out
}

// Kind returns the source's kind and location ("" when no kind key is set).
func (s UpstreamSource) Kind() (kind, location string) {
	for _, k := range s.kinds() {
		if k[1] != "" {
			return k[0], k[1]
		}
	}
	return "", ""
}

// Upstream lifecycle states.
const (
	LifecycleActive    = "active"
	LifecycleDormant   = "dormant"
	LifecycleAbandoned = "abandoned"
	LifecycleGone      = "gone"
)

// EffectiveLifecycle returns the lifecycle (active when unset).
func (s UpstreamSource) EffectiveLifecycle() string {
	if s.Lifecycle == "" {
		return LifecycleActive
	}
	return s.Lifecycle
}

// DefaultUpstreamTags selects tags that start with a dotted version, optionally v-prefixed, so
// commit-SHA and build-id tags that merely start with a digit are not candidates.
const DefaultUpstreamTags = `^v?(?P<version>\d+(?:\.\d+)+.*)$`

// EffectiveTags returns the candidate-tag regex (the default when unset).
func (s UpstreamSelect) EffectiveTags() string {
	if strings.TrimSpace(s.Tags) == "" {
		return DefaultUpstreamTags
	}
	return s.Tags
}

// EffectiveScheme returns the source's versioning scheme (semver when unset).
func (s UpstreamSource) EffectiveScheme() string {
	if s.Scheme != "" {
		return s.Scheme
	}
	switch kind, _ := s.Kind(); kind {
	case UpstreamKindPyPI:
		return scheme.PEP440
	case UpstreamKindApk, UpstreamKindApt:
		return scheme.Debian
	}
	return scheme.Semver
}

// Upstream version modes.
const (
	UpstreamVersionOwn      = "own"
	UpstreamVersionTrack    = "track"
	UpstreamVersionContinue = "continue"
)

// Declared reports whether any upstream source is declared.
func (u UpstreamConfig) Declared() bool { return len(u.Sources) > 0 }

// Mode returns the version mode and, for track and continue, the source id it names. A
// track line pins "id@N"; the id alone is returned here (the pinned major is TrackLine).
func (v UpstreamVersion) Mode() (mode, source string) {
	switch {
	case v.Track != "":
		id, _, _ := splitTrackLine(v.Track)
		return UpstreamVersionTrack, id
	case v.Continue != "":
		return UpstreamVersionContinue, v.Continue
	}
	return UpstreamVersionOwn, ""
}

// TrackLine reports the major version line pinned by the track: id@N escape hatch. pinned
// is false for a plain track: id, which follows the base's current major — read from the
// base itself, not declared here. Pinning a line adopts a new major (id@2 from a 1.x base)
// or holds on one even once a newer major ships.
func (v UpstreamVersion) TrackLine() (major int, pinned bool) {
	if v.Track == "" {
		return 0, false
	}
	_, major, pinned = splitTrackLine(v.Track)
	return major, pinned
}

// splitTrackLine splits a track value "id" or "id@N" into the source id and, when a major
// line is pinned, that major. A non-integer suffix is not a line pin (validateUpstream
// rejects it), so the whole value is returned as the id and surfaces as an unknown source.
func splitTrackLine(s string) (id string, major int, pinned bool) {
	if at := strings.LastIndex(s, "@"); at >= 0 {
		if n, err := strconv.Atoi(s[at+1:]); err == nil {
			return s[:at], n, true
		}
	}
	return s, 0, false
}

// validateUpstream checks the upstream: block.
func validateUpstream(cfg *Config) []string {
	u := cfg.Upstream
	var errs []string
	ids := map[string]UpstreamSource{}
	for _, s := range u.Sources {
		path := "upstream.sources." + s.ID
		ids[s.ID] = s
		var set []string
		for _, k := range s.kinds() {
			if k[1] != "" {
				set = append(set, k[0])
			}
		}
		kind, _ := s.Kind()
		switch {
		case len(set) != 1:
			errs = append(errs, fmt.Sprintf("%s: set exactly one kind (%s), found %d", path, strings.Join(UpstreamKinds(), " | "), len(set)))
		case s.Image != "" && (strings.Contains(s.Image, "@") || strings.Contains(s.Image[strings.LastIndex(s.Image, "/")+1:], ":")):
			errs = append(errs, fmt.Sprintf("%s.image: %q names a tag or digest; give the repository only (its tags are what is watched)", path, s.Image))
		case s.GitHubRelease != "" && strings.Count(s.GitHubRelease, "/") != 1:
			errs = append(errs, fmt.Sprintf("%s.github-release: %q — want owner/repo", path, s.GitHubRelease))
		case s.URL != "" && !strings.HasPrefix(s.URL, "https://") && !strings.HasPrefix(s.URL, "http://"):
			errs = append(errs, fmt.Sprintf("%s.url: %q is not an http(s) URL", path, s.URL))
		case s.URL != "" && s.Select.Tags == "":
			errs = append(errs, path+".select.tags: required for url — it is what finds versions on the page")
		}
		switch kind {
		case UpstreamKindApk, UpstreamKindApt:
			if rel, repo, ok := strings.Cut(s.Suite, "/"); !ok || rel == "" || repo == "" {
				errs = append(errs, fmt.Sprintf("%s.suite: %q — %s needs <release>/<repository>, e.g. v3.22/main or trixie/main", path, s.Suite, kind))
			}
		default:
			if s.Suite != "" {
				errs = append(errs, path+".suite: only used with apk or apt")
			}
		}
		if s.MirrorOf != "" && kind != UpstreamKindGit && kind != UpstreamKindImage {
			errs = append(errs, path+".mirror_of: only git and image sources mirror an origin")
		}
		switch s.Lifecycle {
		case "", LifecycleActive:
			if s.Reason != "" {
				errs = append(errs, path+".reason: only used with a lifecycle other than active")
			}
		case LifecycleDormant, LifecycleAbandoned, LifecycleGone:
			if strings.TrimSpace(s.Reason) == "" {
				errs = append(errs, fmt.Sprintf("%s.reason: required with lifecycle: %s — say what happened upstream", path, s.Lifecycle))
			}
			if s.Lifecycle == LifecycleGone && len(s.Pins) > 0 {
				errs = append(errs, path+".pins: a gone upstream publishes nothing to move pins to")
			}
		default:
			errs = append(errs, fmt.Sprintf("%s.lifecycle: unknown %q (active | dormant | abandoned | gone)", path, s.Lifecycle))
		}
		if s.Select.Tags != "" {
			if _, err := regexp.Compile(s.Select.Tags); err != nil {
				errs = append(errs, fmt.Sprintf("%s.select.tags: %v", path, err))
			}
		}
		for i, ex := range s.Select.Exclude {
			if _, err := regexp.Compile(ex); err != nil {
				errs = append(errs, fmt.Sprintf("%s.select.exclude[%d]: %v", path, i, err))
			}
		}
		for i, rw := range s.Select.Rewrite {
			if rw.From == "" {
				errs = append(errs, fmt.Sprintf("%s.select.rewrite[%d].from: required", path, i))
			} else if _, err := regexp.Compile(rw.From); err != nil {
				errs = append(errs, fmt.Sprintf("%s.select.rewrite[%d].from: %v", path, i, err))
			}
		}
		for i, p := range s.Pins {
			ppath := fmt.Sprintf("%s.pins[%d]", path, i)
			if strings.TrimSpace(p.File) == "" || strings.HasPrefix(p.File, "/") || strings.Contains(p.File, "..") {
				errs = append(errs, fmt.Sprintf("%s.file: %q must be a repo-relative path", ppath, p.File))
			}
			set := 0
			for _, on := range []bool{p.Arg != "", p.Path != "", p.Marker} {
				if on {
					set++
				}
			}
			if set != 1 {
				errs = append(errs, ppath+": set exactly one of arg | path | marker")
			}
			if p.Match != "" {
				if !p.Marker {
					errs = append(errs, ppath+".match: only used with marker")
				} else if _, err := regexp.Compile(p.Match); err != nil {
					errs = append(errs, fmt.Sprintf("%s.match: %v", ppath, err))
				}
			}
		}
		if s.Scheme != "" {
			if _, err := scheme.Get(s.Scheme); err != nil {
				errs = append(errs, fmt.Sprintf("%s.scheme: %v", path, err))
			}
		}
	}

	v := u.Version
	set := 0
	for _, on := range []bool{v.Track != "", v.Continue != "", v.Own != nil} {
		if on {
			set++
		}
	}
	if set > 1 {
		errs = append(errs, "upstream.version: set at most one of track | continue | own")
	}
	mode, src := v.Mode()
	claimed, declared := ids[src]
	if src != "" && !declared {
		errs = append(errs, fmt.Sprintf("upstream.version.%s: %q is not a declared source (upstream.sources)", mode, src))
	}
	if v.Track != "" {
		if at := strings.LastIndex(v.Track, "@"); at >= 0 {
			if n, err := strconv.Atoi(v.Track[at+1:]); err != nil || n < 0 {
				errs = append(errs, fmt.Sprintf("upstream.version.track: %q — a pinned line is track: <source>@<major> with a whole-number major, e.g. %s@1", v.Track, v.Track[:at]))
			}
		}
	}
	if v.MinReleaseAge != nil && mode != UpstreamVersionTrack {
		errs = append(errs, "upstream.version.min_release_age: only meaningful with version.track — it is how old an upstream release must be before the fork follows it onto the base image")
	}
	if declared && claimed.EffectiveLifecycle() == LifecycleGone {
		errs = append(errs, fmt.Sprintf("upstream.version.%s: %q is gone, so no release of ours can be checked against it — use own", mode, src))
	}
	if from, ok := ids[u.Sync.From]; ok && from.EffectiveLifecycle() == LifecycleGone {
		errs = append(errs, fmt.Sprintf("upstream.sync.from: %q is gone; there is nothing to sync", u.Sync.From))
	}
	checkOnMismatch := func(path, on, reason string) {
		switch on {
		case "", UpstreamRefuse:
		case UpstreamReport:
			if strings.TrimSpace(reason) == "" {
				errs = append(errs, path+".reason: required with on_mismatch: report — say why a false version is tolerated")
			}
		default:
			errs = append(errs, fmt.Sprintf("%s.on_mismatch: unknown %q (refuse | report)", path, on))
		}
	}
	checkOnMismatch("upstream.version", v.OnMismatch, v.Reason)
	if sy := u.Sync; sy.declared() {
		from, ok := ids[sy.From]
		switch kind, _ := from.Kind(); {
		case sy.From == "":
			errs = append(errs, "upstream.sync.from: required — the git source to sync from")
		case !ok:
			errs = append(errs, fmt.Sprintf("upstream.sync.from: %q is not a declared source (upstream.sources)", sy.From))
		case kind != UpstreamKindGit:
			errs = append(errs, fmt.Sprintf("upstream.sync.from: %q is not a git source", sy.From))
		}
		switch sy.Strategy {
		case "", SyncRebase, SyncMerge:
		case SyncMirror:
			if sy.Onto.Branch == "" {
				errs = append(errs, "upstream.sync.onto: mirror follows a branch — set onto: {branch: <name>}")
			}
		default:
			errs = append(errs, fmt.Sprintf("upstream.sync.strategy: unknown %q (rebase | merge | mirror)", sy.Strategy))
		}
		for i, f := range sy.Files {
			fpath := fmt.Sprintf("upstream.sync.files[%d]", i)
			if strings.TrimSpace(f.Match) == "" {
				errs = append(errs, fpath+".match: required")
			} else if _, err := filepath.Match(strings.ReplaceAll(f.Match, "**", "*"), ""); err != nil {
				errs = append(errs, fmt.Sprintf("%s.match: %v", fpath, err))
			}
			switch f.Take {
			case TakeMerge, TakeFork, TakeUpstream, TakeReview:
			default:
				errs = append(errs, fmt.Sprintf("%s.take: %q (merge | fork | upstream | review)", fpath, f.Take))
			}
		}
		// A rebase rewrites our branch. A mirror that keeps diverged branches (force off, the
		// default) would silently stop following it; say so here, not after the fork drifts.
		if sy.EffectiveStrategy() == SyncRebase {
			for _, r := range cfg.Repos {
				if b := r.Sync.Branches; b != nil && !b.Force {
					errs = append(errs, fmt.Sprintf("upstream.sync: strategy rebase rewrites the default branch, and repos.%s.sync.branches keeps diverged branches, so %s would stop following it — set repos.%s.sync.branches.force: true, or use strategy: merge", r.ID, r.ID, r.ID))
				}
			}
		}
		if len(sy.Files) > 0 && sy.Strategy == SyncMirror {
			errs = append(errs, "upstream.sync.files: a mirror is an exact copy of upstream; per-file rules apply to rebase and merge")
		}
		switch sy.OnConflict {
		case "", SyncConflictMR, SyncConflictFail, SyncConflictNotify:
		default:
			errs = append(errs, fmt.Sprintf("upstream.sync.on_conflict: unknown %q (mr | fail | notify)", sy.OnConflict))
		}
	}
	switch u.Release {
	case "", UpstreamReleaseAuto, UpstreamReleaseManual:
		if u.Release != "" && mode != UpstreamVersionTrack {
			errs = append(errs, "upstream.release: only meaningful with version.track")
		}
	default:
		errs = append(errs, fmt.Sprintf("upstream.release: unknown %q (auto | manual)", u.Release))
	}
	if v.Ancestry {
		if kind, _ := claimed.Kind(); mode != UpstreamVersionTrack || kind != UpstreamKindGit {
			errs = append(errs, "upstream.version.ancestry: needs track naming a git source")
		}
	}

	dockerBuilds := map[string]bool{}
	for _, b := range cfg.Builds {
		if b.Kind == "docker" {
			dockerBuilds[b.ID] = true
		}
	}
	evidenced := map[string]bool{}
	for _, e := range u.Evidence {
		path := "upstream.evidence." + e.ID
		evidenced[e.ID] = true
		if !dockerBuilds[e.ID] {
			errs = append(errs, path+": not a docker build (builds)")
		}
		if mode == UpstreamVersionOwn {
			errs = append(errs, path+": evidence checks a claimed version; set version.track or version.continue")
		}
		p := e.Probe
		set := 0
		for _, on := range []bool{p.Label != "", p.File != "", p.Package != ""} {
			if on {
				set++
			}
		}
		if set != 1 {
			errs = append(errs, path+".probe: set exactly one of label | file | package")
		}
		switch {
		case p.Package != "" && !validProbeManagers[p.Manager]:
			errs = append(errs, fmt.Sprintf("%s.probe.manager: %q — package needs manager: pip | dpkg | apk", path, p.Manager))
		case p.Package == "" && p.Manager != "":
			errs = append(errs, path+".probe.manager: only meaningful with package")
		}
		for _, stamped := range StampedLabels {
			if p.Label == stamped {
				errs = append(errs, fmt.Sprintf("%s.probe.label: %s is stamped by StageFreight from the release being built, so it always matches the tag and proves nothing — probe what the image installed (package, file) or a label its own build sets", path, stamped))
			}
		}
		if p.File != "" && !strings.HasPrefix(p.File, "/") {
			errs = append(errs, fmt.Sprintf("%s.probe.file: %q must be an absolute path in the image", path, p.File))
		}
		if p.Match != "" {
			if p.Package != "" {
				errs = append(errs, path+".probe.match: not used with package (the database names the version)")
			} else if _, err := regexp.Compile(p.Match); err != nil {
				errs = append(errs, fmt.Sprintf("%s.probe.match: %v", path, err))
			}
		}
		checkOnMismatch(path, e.OnMismatch, e.Reason)
	}
	// No evidence, no track: a tracked version must be proven for every image that ships it,
	// by a probe or by ancestry.
	if mode == UpstreamVersionTrack && declared && !v.Ancestry {
		for _, b := range cfg.Builds {
			if b.Kind == "docker" && !evidenced[b.ID] {
				errs = append(errs, fmt.Sprintf("upstream.evidence.%s: version.track needs proof the image contains the version — add a probe (label | file | package) for this build, or set version.ancestry: true for a history fork", b.ID))
			}
		}
	}

	if v.Since != "" {
		if mode == UpstreamVersionOwn {
			errs = append(errs, "upstream.version.since: only meaningful with track or continue")
		} else if sch, err := scheme.Get(claimed.EffectiveScheme()); declared && err == nil {
			if _, err := sch.Parse(v.Since); err != nil {
				errs = append(errs, fmt.Sprintf("upstream.version.since: %q is not a %s version", v.Since, sch.Name()))
			}
		}
	}
	return errs
}
