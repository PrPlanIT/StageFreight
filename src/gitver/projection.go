package gitver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// When a breaking change projects the next major (git.versioning.majors).
const (
	MajorsAlways  = "always"   // every breaking change, 1.0.0 included
	MajorsAfterV1 = "after_v1" // from 1.0 on; before 1.0 it projects the minor
	MajorsNever   = "never"    // never; a major is always tagged by hand
)

// DefaultBranchBuildFormat is the version of a non-release commit when its rule declares none.
const DefaultBranchBuildFormat = "{next}-dev.{commits}+{sha}"

// usesProjection reports whether a format needs the commits since its base.
func usesProjection(format string) bool {
	return strings.Contains(format, "{next}") || strings.Contains(format, "{commits}")
}

// project sets v.Next and v.Commits from the commits between tag and HEAD. A prerelease
// base already names the release it leads to, so its core version is the next one.
func project(repo *git.Repository, v *VersionInfo, tag, majors string) error {
	if v.Major == "" {
		return fmt.Errorf("versioning: {next} needs a semver base tag; %q is not one", tag)
	}
	commits, err := commitsSince(repo, tag)
	if err != nil {
		return err
	}
	v.Commits = len(commits)
	if v.IsPrerelease {
		v.Next = v.Base
		return nil
	}
	major, _ := strconv.Atoi(v.Major)
	minor, _ := strconv.Atoi(v.Minor)
	patch, _ := strconv.Atoi(v.Patch)
	bump := bumpPatch
	for _, c := range commits {
		bump = max(bump, bumpOf(ParseConventional(c.Message), major, majors))
	}
	switch bump {
	case bumpMajor:
		major, minor, patch = major+1, 0, 0
	case bumpMinor:
		minor, patch = minor+1, 0
	default:
		patch++
	}
	v.Next = fmt.Sprintf("%d.%d.%d", major, minor, patch)
	return nil
}

const (
	bumpPatch = iota
	bumpMinor
	bumpMajor
)

func bumpOf(c Conventional, major int, majors string) int {
	if c.Breaking {
		if majors == MajorsAlways || (majors != MajorsNever && major >= 1) {
			return bumpMajor
		}
		return bumpMinor
	}
	if c.Type == "feat" {
		return bumpMinor
	}
	return bumpPatch
}

// commitsSince returns the commits reachable from HEAD and not from tag (git rev-list tag..HEAD).
func commitsSince(repo *git.Repository, tag string) ([]*object.Commit, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, err
	}
	tagHash, err := repo.ResolveRevision(plumbing.Revision(tag + "^{commit}"))
	if err != nil {
		return nil, fmt.Errorf("versioning: resolving %s: %w", tag, err)
	}
	seen := map[plumbing.Hash]bool{}
	if err := walk(repo, *tagHash, nil, func(c *object.Commit) { seen[c.Hash] = true }); err != nil {
		return nil, shallowHint(tag, err)
	}
	var out []*object.Commit
	if err := walk(repo, head.Hash(), seen, func(c *object.Commit) { out = append(out, c) }); err != nil {
		return nil, shallowHint(tag, err)
	}
	return out, nil
}

func walk(repo *git.Repository, from plumbing.Hash, seen map[plumbing.Hash]bool, visit func(*object.Commit)) error {
	start, err := repo.CommitObject(from)
	if err != nil {
		return err
	}
	return object.NewCommitPreorderIter(start, seen, nil).ForEach(func(c *object.Commit) error {
		visit(c)
		return nil
	})
}

func shallowHint(tag string, err error) error {
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		return fmt.Errorf("versioning: {next}/{commits} need the history since %s, which this clone lacks (clone with full history, e.g. GIT_DEPTH: 0): %w", tag, err)
	}
	return fmt.Errorf("versioning: walking the commits since %s: %w", tag, err)
}
