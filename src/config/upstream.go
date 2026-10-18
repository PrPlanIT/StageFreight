package config

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

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
	// Label is an OCI image label whose value is the version, e.g.
	// org.opencontainers.image.version.
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
	// source never released is refused.
	Track string `yaml:"track,omitempty"`

	// Continue names the source whose numbering our own lineage continues (a dormant or
	// diverged upstream). A release naming a version that source has released is refused.
	Continue string `yaml:"continue,omitempty"`

	// Own declares our own version lineage: upstreams are watched, never enforced. The
	// default when neither track nor continue is set.
	Own *struct{} `yaml:"own,omitempty"`

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

// Upstream source kinds.
const (
	UpstreamKindGit   = "git"
	UpstreamKindImage = "image"
)

// Kind returns the source's kind and location ("" when no kind key is set).
func (s UpstreamSource) Kind() (kind, location string) {
	switch {
	case s.Git != "":
		return UpstreamKindGit, s.Git
	case s.Image != "":
		return UpstreamKindImage, s.Image
	}
	return "", ""
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
	if s.Scheme == "" {
		return scheme.Semver
	}
	return s.Scheme
}

// Upstream version modes.
const (
	UpstreamVersionOwn      = "own"
	UpstreamVersionTrack    = "track"
	UpstreamVersionContinue = "continue"
)

// Declared reports whether any upstream source is declared.
func (u UpstreamConfig) Declared() bool { return len(u.Sources) > 0 }

// Mode returns the version mode and, for track and continue, the source id it names.
func (v UpstreamVersion) Mode() (mode, source string) {
	switch {
	case v.Track != "":
		return UpstreamVersionTrack, v.Track
	case v.Continue != "":
		return UpstreamVersionContinue, v.Continue
	}
	return UpstreamVersionOwn, ""
}

// validateUpstream checks the upstream: block.
func validateUpstream(cfg *Config) []string {
	u := cfg.Upstream
	var errs []string
	ids := map[string]UpstreamSource{}
	for _, s := range u.Sources {
		path := "upstream.sources." + s.ID
		ids[s.ID] = s
		switch {
		case s.Git != "" && s.Image != "":
			errs = append(errs, path+": set exactly one kind (git | image), found both")
		case s.Git == "" && s.Image == "":
			errs = append(errs, path+": set exactly one kind (git | image)")
		case s.Image != "" && (strings.Contains(s.Image, "@") || strings.Contains(s.Image[strings.LastIndex(s.Image, "/")+1:], ":")):
			errs = append(errs, fmt.Sprintf("%s.image: %q names a tag or digest; give the repository only (its tags are what is watched)", path, s.Image))
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
