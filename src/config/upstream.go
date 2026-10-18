package config

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
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
}

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

	// Prereleases admits upstream prereleases (1.2.0-rc.1) as releases. Default false.
	Prereleases bool `yaml:"prereleases,omitempty"`
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
func validateUpstream(u UpstreamConfig) []string {
	var errs []string
	ids := map[string]bool{}
	for _, s := range u.Sources {
		path := "upstream.sources." + s.ID
		ids[s.ID] = true
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
	if src != "" && !ids[src] {
		errs = append(errs, fmt.Sprintf("upstream.version.%s: %q is not a declared source (upstream.sources)", mode, src))
	}
	if v.Since != "" {
		if mode == UpstreamVersionOwn {
			errs = append(errs, "upstream.version.since: only meaningful with track or continue")
		} else if version.ParseVersion(v.Since) == nil {
			errs = append(errs, fmt.Sprintf("upstream.version.since: %q is not a version", v.Since))
		}
	}
	return errs
}
