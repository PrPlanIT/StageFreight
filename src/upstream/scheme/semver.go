package scheme

import (
	"strings"

	masterminds "github.com/Masterminds/semver/v3"
)

// semverScheme is Semantic Versioning, read leniently: a missing minor or patch is zero
// (11.4 is 11.4.0), the v prefix is optional, and a hyphen suffix is a prerelease.
type semverScheme struct{}

type semverVersion struct{ v *masterminds.Version }

func (semverScheme) Name() string { return Semver }

func (semverScheme) Parse(s string) (Version, error) {
	v, err := masterminds.NewVersion(strings.TrimPrefix(s, "v"))
	if err != nil {
		return nil, err
	}
	return semverVersion{v}, nil
}

func (v semverVersion) String() string        { return v.v.String() }
func (v semverVersion) Prerelease() bool      { return v.v.Prerelease() != "" }
func (v semverVersion) Compare(o Version) int { return v.v.Compare(o.(semverVersion).v) }
