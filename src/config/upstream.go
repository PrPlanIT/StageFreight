package config

import (
	"fmt"
	"regexp"
	"strings"
)

// UpstreamConfig declares the authoritative source a fork repackages ("fork mode"). Declaring
// it makes StageFreight watch that source for new releases and, with version: track, refuse to
// release a version the source never published.
type UpstreamConfig struct {
	// Git lists upstream git repositories whose release tags are watched, for forks that build
	// from source.
	Git []UpstreamGit `yaml:"git,omitempty"`

	// Images lists upstream container image repositories whose tags are watched, for forks that
	// repackage a published image.
	Images []UpstreamImage `yaml:"images,omitempty"`

	// Version says what this repo's release version means.
	//   own   (default): the repo keeps its own version lineage; upstream releases are watched
	//                    and reported, never enforced.
	//   track:           the release version IS the upstream version. A release whose version
	//                    upstream never published is refused before anything is built.
	// track needs exactly one source, so there is no doubt which upstream the version names.
	Version string `yaml:"version,omitempty"`
}

// UpstreamGit is one watched upstream git repository.
type UpstreamGit struct {
	// Repo is the repository URL, e.g. https://github.com/ansible/ansible. Read anonymously.
	Repo string `yaml:"repo"`

	// Match is a regex selecting the upstream's release tags. The version is the named group
	// "version", else the first group, else the whole tag. Default: stable semver tags,
	// optionally v-prefixed.
	Match string `yaml:"match,omitempty"`
}

// UpstreamImage is one watched upstream container image repository.
type UpstreamImage struct {
	// Ref is the image repository without a tag, e.g. ghcr.io/zitadel/zitadel or
	// docker.io/library/python. Read anonymously.
	Ref string `yaml:"ref"`

	// Match is a regex selecting the upstream's release tags; same rules as UpstreamGit.Match.
	Match string `yaml:"match,omitempty"`
}

// DefaultUpstreamMatch selects stable semver release tags, optionally v-prefixed.
const DefaultUpstreamMatch = `^v?(\d+\.\d+\.\d+)$`

// Upstream version modes.
const (
	UpstreamVersionOwn   = "own"
	UpstreamVersionTrack = "track"
)

// Declared reports whether any upstream source is declared.
func (u UpstreamConfig) Declared() bool {
	return len(u.Git) > 0 || len(u.Images) > 0
}

// VersionMode returns the effective version mode (own when unset).
func (u UpstreamConfig) VersionMode() string {
	if u.Version == "" {
		return UpstreamVersionOwn
	}
	return u.Version
}

// EffectiveMatch returns the tag-selection regex for a source (the default when unset).
func EffectiveMatch(match string) string {
	if strings.TrimSpace(match) == "" {
		return DefaultUpstreamMatch
	}
	return match
}

// validateUpstream checks the upstream: block.
func validateUpstream(u UpstreamConfig) []string {
	var errs []string
	switch u.Version {
	case "", UpstreamVersionOwn, UpstreamVersionTrack:
	default:
		errs = append(errs, fmt.Sprintf("upstream.version: unknown mode %q (own | track)", u.Version))
	}
	if u.Version != "" && !u.Declared() {
		errs = append(errs, "upstream.version: set, but no upstream.git or upstream.images source is declared")
	}
	if u.VersionMode() == UpstreamVersionTrack && len(u.Git)+len(u.Images) != 1 {
		errs = append(errs, fmt.Sprintf("upstream.version: track needs exactly one source so the version names one upstream (found %d)", len(u.Git)+len(u.Images)))
	}
	checkMatch := func(path, match string) {
		if match == "" {
			return
		}
		if _, err := regexp.Compile(match); err != nil {
			errs = append(errs, fmt.Sprintf("%s.match: %v", path, err))
		}
	}
	for i, g := range u.Git {
		path := fmt.Sprintf("upstream.git[%d]", i)
		if strings.TrimSpace(g.Repo) == "" {
			errs = append(errs, path+".repo: required")
		}
		checkMatch(path, g.Match)
	}
	for i, im := range u.Images {
		path := fmt.Sprintf("upstream.images[%d]", i)
		switch {
		case strings.TrimSpace(im.Ref) == "":
			errs = append(errs, path+".ref: required")
		case strings.Contains(im.Ref, "@") || strings.Contains(im.Ref[strings.LastIndex(im.Ref, "/")+1:], ":"):
			errs = append(errs, fmt.Sprintf("%s.ref: %q names a tag or digest; give the repository only (tags are what is watched)", path, im.Ref))
		}
		checkMatch(path, im.Match)
	}
	return errs
}
