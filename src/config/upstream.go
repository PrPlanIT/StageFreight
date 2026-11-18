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

// UpstreamConfig declares the upstreams a repo repackages or forks.
type UpstreamConfig struct {
	// Sources are the watched upstreams, keyed by id. Renovate annotations in the docker
	// builds' Dockerfiles declare sources too.
	Sources OrderedUpstreamSources `yaml:"sources,omitempty"`

	// Version is what this repo's release tags claim about an upstream.
	Version UpstreamVersion `yaml:"version,omitempty"`

	// Sync keeps this repo's history on top of a git upstream.
	Sync UpstreamSync `yaml:"sync,omitempty"`

	// Evidence proves, per docker build id, that the image contains the claimed version.
	Evidence OrderedUpstreamEvidence `yaml:"evidence,omitempty"`
}

// UpstreamEvidence is one docker build's proof of version.
type UpstreamEvidence struct {
	ID string `yaml:"-"`

	Probe UpstreamProbe `yaml:",inline"`

	// OnMismatch: refuse (default) withholds the image; report ships it and raises
	// version-mismatch.
	OnMismatch string `yaml:"on_mismatch,omitempty"`

	// Reason says why a mismatch is tolerated. Required with on_mismatch: report.
	Reason string `yaml:"reason,omitempty"`

	// Derived marks evidence StageFreight took from the tracked package source.
	Derived bool `yaml:"-"`
}

// UpstreamProbe reads a version out of a built image without running it. Set exactly one
// of label, file, package.
type UpstreamProbe struct {
	// Label is an OCI label the image's own build sets; labels StageFreight stamps are refused.
	Label string `yaml:"label,omitempty"`

	// File is an absolute path in the image holding the version.
	File string `yaml:"file,omitempty"`

	// Package is an installed package, read from the database manager names.
	Package string `yaml:"package,omitempty"`

	// Manager is pip, dpkg or apk.
	Manager string `yaml:"manager,omitempty"`

	// Match is a regex on the label or file whose version group is the version. Default: the
	// first dotted version.
	Match string `yaml:"match,omitempty"`
}

// OrderedUpstreamEvidence is upstream.evidence — a build id→evidence map.
type OrderedUpstreamEvidence []UpstreamEvidence

func (OrderedUpstreamEvidence) isIDMap() {}

func (o *OrderedUpstreamEvidence) UnmarshalYAML(n *yaml.Node) error {
	if err := movedKeys(n, "upstream.evidence.<build>", map[string]string{"probe": "its keys (label, file, package, manager, match) sit on the evidence entry itself"}, true); err != nil {
		return err
	}
	v, err := decodeIDMap(n, func(e *UpstreamEvidence, id string) { e.ID = id })
	if err != nil {
		return fmt.Errorf("upstream.evidence: %w", err)
	}
	*o = v
	return nil
}

// Cut modes.
const (
	UpstreamCutAuto   = "auto"
	UpstreamCutManual = "manual"
)

// Recut modes.
const (
	UpstreamRecutOnChange = "on_change"
	UpstreamRecutNever    = "never"
)

// AutoRecut reports whether StageFreight moves an already-released version tag to a newer
// build whose inputs changed.
func (u UpstreamConfig) AutoRecut() bool {
	return u.AutoRelease() && u.Version.Recut != UpstreamRecutNever
}

// AutoRelease reports whether StageFreight cuts track releases itself.
func (u UpstreamConfig) AutoRelease() bool {
	mode, _ := u.Version.Mode()
	return mode == UpstreamVersionTrack && u.Version.Cut != UpstreamCutManual
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
// build's own claims (the version being built, its commit, its time, its identity/lineage), so
// they can never be evidence of what the image contains — a version-evidence probe on one of
// these would always agree with the tag and prove nothing. Kept in lockstep with
// build.ImageLabels (which subsumes build.StandardLabels) by a test in the build package.
var StampedLabels = []string{
	"org.opencontainers.image.created",
	"org.opencontainers.image.revision",
	"org.opencontainers.image.version",
	"org.opencontainers.image.description",
	"org.opencontainers.image.title",
	"org.opencontainers.image.source",
	"org.opencontainers.image.url",
	"org.opencontainers.image.documentation",
	"org.opencontainers.image.licenses",
	"org.opencontainers.image.vendor",
	"org.opencontainers.image.authors",
	"org.opencontainers.image.base.name",
	"org.opencontainers.image.base.digest",
	"org.stagefreight.build.mode",
	"org.stagefreight.plan.sha256",
	"org.stagefreight.maintenance",
	"org.stagefreight.build.number",
}

// Probe package managers.
var validProbeManagers = map[string]bool{"pip": true, "dpkg": true, "apk": true}

// UpstreamSource is one watched upstream. Exactly one kind key names it.
type UpstreamSource struct {
	ID string `yaml:"-"`

	// Git is a git repository URL; its tags are listed anonymously.
	Git string `yaml:"git,omitempty"`

	// ForkParent names a repos: entry; the project its forge says it was forked from is the
	// git upstream. Fails closed when the forge will not say.
	ForkParent string `yaml:"fork_parent,omitempty"`

	// ForkParentGit is the clone URL fork_parent resolved to (run time only).
	ForkParentGit string `yaml:"-"`

	// ForkParentErr is why fork_parent did not resolve (run time only).
	ForkParentErr string `yaml:"-"`

	// Image is a container image repository without a tag.
	Image string `yaml:"image,omitempty"`

	// PyPI is a project on pypi.org. Scheme pep440.
	PyPI string `yaml:"pypi,omitempty"`

	// Npm is a package on registry.npmjs.org.
	Npm string `yaml:"npm,omitempty"`

	// GitHubRelease is an owner/repo whose published GitHub releases are watched.
	GitHubRelease string `yaml:"github_release,omitempty"`

	// Apk is an Alpine package. Needs suite or repository. Scheme debian.
	Apk string `yaml:"apk,omitempty"`

	// Apt is a Debian package. Needs suite. Scheme debian.
	Apt string `yaml:"apt,omitempty"`

	// URL is a release page; every match of tags on it is a candidate. Needs tags.
	URL string `yaml:"url,omitempty"`

	// Suite (apk, apt) is <release>/<repository>, e.g. v3.22/main or trixie/main.
	Suite string `yaml:"suite,omitempty"`

	// Repository (apk) is main or community, with the release read from the Dockerfile FROM
	// that holds the first pin; the pin moves with that FROM. Replaces suite.
	Repository string `yaml:"repository,omitempty"`

	// MirrorOf (git, image) is the origin this source mirrors; lagging it raises mirror-behind.
	MirrorOf string `yaml:"mirror_of,omitempty"`

	// Lifecycle is active (default), dormant, abandoned or gone. Anything else needs a reason.
	Lifecycle string `yaml:"lifecycle,omitempty"`

	// Reason says why an upstream is not active.
	Reason string `yaml:"reason,omitempty"`

	// Select picks the upstream's releases out of everything it tags.
	Select UpstreamSelect `yaml:",inline"`

	// Scheme reads and orders versions: semver (default), pep440 or loose.
	Scheme string `yaml:"scheme,omitempty"`

	// MinReleaseAge is how old a release must be before this source's pins, and a tracked
	// base image, move to it. Unset inherits dependency.min_release_age; 0 follows at once.
	MinReleaseAge *supplychain.Cooldown `yaml:"min_release_age,omitempty"`

	// Pins are where this upstream's version is pinned; they move together or not at all.
	Pins []UpstreamPin `yaml:"pins,omitempty"`

	// Annotation is the file:line of the renovate annotation that declared this source.
	Annotation string `yaml:"-"`

	registry *UpstreamAnnotation
}

// UpstreamSync is upstream.sync.
type UpstreamSync struct {
	// From names the git source to sync from. Default: the only git source.
	From string `yaml:"from,omitempty"`

	// Branch is the upstream branch to sync onto: unset follows upstream's newest release,
	// HEAD its default branch.
	Branch string `yaml:"branch,omitempty"`

	// Strategy is rebase (default; our branch is rewritten), merge, or mirror (fast-forward
	// only; a diverged branch is kept and reported).
	Strategy string `yaml:"strategy,omitempty"`

	// Shadow, when set, is a branch kept at upstream's synced commit.
	Shadow string `yaml:"shadow,omitempty"`

	// OnConflict is mr (default; a merge request for a human), fail, or notify.
	OnConflict string `yaml:"on_conflict,omitempty"`

	// Files decide per path what wins when upstream changes a file; the last matching rule
	// wins. StageFreight's own files always keep the fork's version.
	Files []UpstreamFileRule `yaml:"files,omitempty"`
}

// UpstreamFileRule is one upstream.sync.files rule.
type UpstreamFileRule struct {
	// Match is a path glob; ** spans directories.
	Match string `yaml:"match"`

	// Take is merge (default), fork (keep ours), upstream (take theirs), or review (always a
	// merge request).
	Take string `yaml:"take"`
}

// Sync file outcomes.
const (
	TakeMerge    = "merge"
	TakeFork     = "fork"
	TakeUpstream = "upstream"
	TakeReview   = "review"
)

// UpstreamOnto is what a sync lands on: upstream's newest release (zero value), its
// default branch (Head), or a named branch.
type UpstreamOnto struct {
	Branch string
	Head   bool
}

// UnmarshalYAML reads branch into Onto and names the moved onto key.
func (s *UpstreamSync) UnmarshalYAML(n *yaml.Node) error {
	if err := movedKeys(n, "upstream.sync", map[string]string{"onto": "upstream.sync.branch (unset follows the newest release, HEAD the default branch)"}, false); err != nil {
		return err
	}
	type plain UpstreamSync
	var p plain
	if err := strictNodeDecode(n, &p); err != nil {
		return fmt.Errorf("upstream.sync: %w", err)
	}
	*s = UpstreamSync(p)
	return nil
}

// Onto reads branch: upstream's newest release when unset, its default branch for HEAD.
func (s UpstreamSync) Onto() UpstreamOnto {
	switch s.Branch {
	case "":
		return UpstreamOnto{}
	case "HEAD":
		return UpstreamOnto{Head: true}
	}
	return UpstreamOnto{Branch: s.Branch}
}

// FollowsBranch reports whether onto names a branch (head or {branch:}) rather than a
// release.
func (o UpstreamOnto) FollowsBranch() bool { return o.Head || o.Branch != "" }

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
	return s.From != "" || s.Branch != "" || s.Strategy != "" || s.Shadow != "" || s.OnConflict != "" || len(s.Files) > 0
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

	// Path is a dotted path to a scalar in a YAML or JSON file, e.g. apps.0.tag.
	Path string `yaml:"path,omitempty"`

	// Marker pins the line after a "stagefreight: upstream=<source id>" comment.
	Marker bool `yaml:"marker,omitempty"`

	// Match (marker only) is a regex whose version group is the pinned version. Default: the
	// first dotted version on the line.
	Match string `yaml:"match,omitempty"`
}

// UpstreamSelect picks releases from an upstream's tags: candidates, minus exclusions, minus
// prereleases unless admitted. A candidate whose version does not parse is reported.
type UpstreamSelect struct {
	// Tags is a regex choosing candidate tags; its version group is the version. Default:
	// tags starting with a dotted version, optionally v-prefixed.
	Tags string `yaml:"tags,omitempty"`

	// Exclude lists regexes for tags that are never releases.
	Exclude []string `yaml:"exclude,omitempty"`

	// Prereleases admits upstream prereleases.
	Prereleases bool `yaml:"prereleases,omitempty"`

	// Rewrite edits the extracted version before parsing, in order, e.g. {from: '_', to: '.'}.
	Rewrite []UpstreamRewrite `yaml:"rewrite,omitempty"`
}

// UpstreamRewrite is one select.rewrite step.
type UpstreamRewrite struct {
	From string `yaml:"from"`
	To   string `yaml:"to,omitempty"` // empty deletes the match
}

// UpstreamVersion is what our release tags claim. Set at most one of track, continue, own;
// none means own.
type UpstreamVersion struct {
	// Track names the source whose version our release is; a version it never released is
	// refused. For an image source it also drives the base image within its major; <source>@N
	// pins a major line.
	Track string `yaml:"track,omitempty"`

	// Continue names the source whose numbering our own lineage continues; a version it has
	// released is refused.
	Continue string `yaml:"continue,omitempty"`

	// Own declares our own lineage: upstreams are watched, never enforced.
	Own *struct{} `yaml:"own,omitempty"`

	// Since is the cutover tag: only release tags at or after it are judged.
	Since string `yaml:"since,omitempty"`

	// Ancestry (track, git source) requires upstream's commit for the claimed version to be an
	// ancestor of our release commit. It is evidence in place of a probe.
	Ancestry bool `yaml:"ancestry,omitempty"`

	// Cut (track) is auto (default; StageFreight tags a built commit that names a new upstream
	// version) or manual.
	Cut string `yaml:"cut,omitempty"`

	// Recut (cut: auto) is on_change (default; a released version moves to a newer build whose
	// inputs differ) or never.
	Recut string `yaml:"recut,omitempty"`

	// OnMismatch: refuse (default) blocks a release whose claim is false; report ships it and
	// raises version-mismatch.
	OnMismatch string `yaml:"on_mismatch,omitempty"`

	// Reason says why a false claim is tolerated. Required with on_mismatch: report.
	Reason string `yaml:"reason,omitempty"`
}

// UnmarshalYAML names the moved min_release_age key, then decodes strictly.
func (v *UpstreamVersion) UnmarshalYAML(n *yaml.Node) error {
	if err := movedKeys(n, "upstream.version", map[string]string{"min_release_age": "upstream.sources.<id>.min_release_age"}, false); err != nil {
		return err
	}
	type plain UpstreamVersion
	var p plain
	if err := strictNodeDecode(n, &p); err != nil {
		return fmt.Errorf("upstream.version: %w", err)
	}
	*v = UpstreamVersion(p)
	return nil
}

// OrderedUpstreamSources is upstream.sources — an id→source map (key becomes UpstreamSource.ID).
type OrderedUpstreamSources []UpstreamSource

func (OrderedUpstreamSources) isIDMap() {}

func (o *OrderedUpstreamSources) UnmarshalYAML(n *yaml.Node) error {
	if err := movedKeys(n, "upstream.sources.<id>", map[string]string{
		"select":         "its keys (tags, exclude, prereleases, rewrite) sit on the source itself",
		"github-release": "github_release",
	}, true); err != nil {
		return err
	}
	v, err := decodeIDMap(n, func(s *UpstreamSource, id string) { s.ID = id })
	if err != nil {
		return fmt.Errorf("upstream.sources: %w", err)
	}
	*o = v
	return nil
}

// movedKeys names a retired key's new home. With entries set, n is an id map and each entry
// is checked.
func movedKeys(n *yaml.Node, path string, moves map[string]string, entries bool) error {
	check := func(m *yaml.Node) error {
		if m.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if to, ok := moves[m.Content[i].Value]; ok {
				return fmt.Errorf("%s.%s: moved — %s", path, m.Content[i].Value, to)
			}
		}
		return nil
	}
	if !entries {
		return check(n)
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if err := check(n.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalYAML rejects retired shapes with their replacement as the error, then decodes
// strictly.
func (u *UpstreamConfig) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i].Value, n.Content[i+1]
			switch {
			case key == "git" || key == "images":
				return fmt.Errorf("upstream.%s: sources are now an id-keyed map: upstream.sources.<id>: {git: <url>} or {image: <repository>}, with match: moved to select.tags", key)
			case key == "release":
				return fmt.Errorf("upstream.release: moved to upstream.version.cut")
			case key == "recut":
				return fmt.Errorf("upstream.recut: moved to upstream.version.recut")
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
	UpstreamKindGitHubRelease = "github_release"
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
	if s.ForkParent != "" && s.Git == "" {
		return UpstreamKindGit, s.ForkParentGit
	}
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
		if s.ForkParent != "" {
			set = append(set, "fork_parent")
		}
		kind, _ := s.Kind()
		switch {
		case len(set) != 1:
			errs = append(errs, fmt.Sprintf("%s: set exactly one kind (%s | fork_parent), found %d", path, strings.Join(UpstreamKinds(), " | "), len(set)))
		case s.ForkParent != "" && FindRepoByID(cfg.Repos, s.ForkParent) == nil:
			errs = append(errs, fmt.Sprintf("%s.fork_parent: %q is not a repos: entry", path, s.ForkParent))
		case s.Image != "" && (strings.Contains(s.Image, "@") || strings.Contains(s.Image[strings.LastIndex(s.Image, "/")+1:], ":")):
			errs = append(errs, fmt.Sprintf("%s.image: %q names a tag or digest; give the repository only (its tags are what is watched)", path, s.Image))
		case s.GitHubRelease != "" && strings.Count(s.GitHubRelease, "/") != 1:
			errs = append(errs, fmt.Sprintf("%s.github_release: %q — want owner/repo", path, s.GitHubRelease))
		case s.URL != "" && !strings.HasPrefix(s.URL, "https://") && !strings.HasPrefix(s.URL, "http://"):
			errs = append(errs, fmt.Sprintf("%s.url: %q is not an http(s) URL", path, s.URL))
		case s.URL != "" && s.Select.Tags == "":
			errs = append(errs, path+".tags: required for url — it is what finds versions on the page")
		}
		switch {
		case s.Repository != "" && kind != UpstreamKindApk:
			errs = append(errs, path+".repository: only used with apk")
		case s.Repository != "" && s.Suite != "":
			errs = append(errs, path+": set suite or repository, not both")
		case s.Repository != "" && (len(s.Pins) == 0 || s.Pins[0].Arg == ""):
			errs = append(errs, path+".repository: the first pin must be a Dockerfile arg, whose FROM names the Alpine release")
		case s.Repository != "":
		case kind == UpstreamKindApk || kind == UpstreamKindApt:
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
				errs = append(errs, fmt.Sprintf("%s.tags: %v", path, err))
			}
		}
		for i, ex := range s.Select.Exclude {
			if _, err := regexp.Compile(ex); err != nil {
				errs = append(errs, fmt.Sprintf("%s.exclude[%d]: %v", path, i, err))
			}
		}
		for i, rw := range s.Select.Rewrite {
			if rw.From == "" {
				errs = append(errs, fmt.Sprintf("%s.rewrite[%d].from: required", path, i))
			} else if _, err := regexp.Compile(rw.From); err != nil {
				errs = append(errs, fmt.Sprintf("%s.rewrite[%d].from: %v", path, i, err))
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
			if !sy.Onto().FollowsBranch() {
				errs = append(errs, "upstream.sync.branch: mirror follows a branch — set branch: HEAD or a branch name")
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
	switch v.Cut {
	case "", UpstreamCutAuto, UpstreamCutManual:
		if v.Cut != "" && mode != UpstreamVersionTrack {
			errs = append(errs, "upstream.version.cut: only meaningful with track")
		}
	default:
		errs = append(errs, fmt.Sprintf("upstream.version.cut: unknown %q (auto | manual)", v.Cut))
	}
	switch v.Recut {
	case "", UpstreamRecutOnChange, UpstreamRecutNever:
		if v.Recut != "" && !u.AutoRelease() {
			errs = append(errs, "upstream.version.recut: only meaningful with track and cut: auto")
		}
	default:
		errs = append(errs, fmt.Sprintf("upstream.version.recut: unknown %q (on_change | never)", v.Recut))
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
			errs = append(errs, path+": set exactly one of label | file | package")
		}
		switch {
		case p.Package != "" && !validProbeManagers[p.Manager]:
			errs = append(errs, fmt.Sprintf("%s.manager: %q — package needs manager: pip | dpkg | apk", path, p.Manager))
		case p.Package == "" && p.Manager != "":
			errs = append(errs, path+".manager: only meaningful with package")
		}
		for _, stamped := range StampedLabels {
			if p.Label == stamped {
				errs = append(errs, fmt.Sprintf("%s.label: %s is stamped by StageFreight from the release being built, so it always matches the tag and proves nothing — probe what the image installed (package, file) or a label its own build sets", path, stamped))
			}
		}
		if p.File != "" && !strings.HasPrefix(p.File, "/") {
			errs = append(errs, fmt.Sprintf("%s.file: %q must be an absolute path in the image", path, p.File))
		}
		if p.Match != "" {
			if p.Package != "" {
				errs = append(errs, path+".match: not used with package (the database names the version)")
			} else if _, err := regexp.Compile(p.Match); err != nil {
				errs = append(errs, fmt.Sprintf("%s.match: %v", path, err))
			}
		}
		checkOnMismatch(path, e.OnMismatch, e.Reason)
	}
	// No evidence, no track: a tracked version must be proven for every image that ships it,
	// by a probe or by ancestry.
	if mode == UpstreamVersionTrack && declared && !v.Ancestry {
		for _, b := range cfg.Builds {
			if b.Kind == "docker" && !evidenced[b.ID] {
				errs = append(errs, fmt.Sprintf("upstream.evidence.%s: version.track needs proof the image contains the version — add evidence (label | file | package) for this build, or set version.ancestry: true for a history fork", b.ID))
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
