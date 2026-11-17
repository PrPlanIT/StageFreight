package cmd

import (
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// buildInputsDigest is the content identity of everything a commit's builds are made from,
// read from the commit's tree (no checkout): a docker build's Dockerfile and the files of its
// context that .dockerignore admits; a binary or command build's whole tree. StageFreight's
// own state (.stagefreight/) and .git never count. Two commits with the same digest ship the
// same release, so a recut between them would only churn.
func buildInputsDigest(repo *git.Repository, commit plumbing.Hash, builds []config.BuildConfig) (string, error) {
	c, err := repo.CommitObject(commit)
	if err != nil {
		return "", fmt.Errorf("reading commit %s: %w", commit, err)
	}
	tree, err := c.Tree()
	if err != nil {
		return "", fmt.Errorf("reading tree of %s: %w", commit, err)
	}
	files := map[string]plumbing.Hash{}
	if err := tree.Files().ForEach(func(f *object.File) error {
		files[f.Name] = f.Hash
		return nil
	}); err != nil {
		return "", fmt.Errorf("listing tree of %s: %w", commit, err)
	}

	h := sha256.New()
	for _, b := range builds {
		paths, err := buildInputPaths(repo, files, b)
		if err != nil {
			return "", fmt.Errorf("build %s: %w", b.ID, err)
		}
		fmt.Fprintf(h, "build:%s:%s\n", b.ID, b.Kind)
		for _, p := range paths {
			fmt.Fprintf(h, "f:%s:%s\n", p, files[p])
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// buildInputPaths lists, sorted, the tree paths one build is made from.
func buildInputPaths(repo *git.Repository, files map[string]plumbing.Hash, b config.BuildConfig) ([]string, error) {
	var out []string
	if b.Kind != "docker" {
		for p := range files {
			if !neverInput(p) {
				out = append(out, p)
			}
		}
		sort.Strings(out)
		return out, nil
	}

	ctx := cleanTreePath(b.Context)
	prefix := ""
	if ctx != "" {
		prefix = ctx + "/"
	}
	var ignore *patternmatcher.PatternMatcher
	if id, ok := files[prefix+".dockerignore"]; ok {
		patterns, err := readIgnorePatterns(repo, id)
		if err != nil {
			return nil, err
		}
		if ignore, err = patternmatcher.New(patterns); err != nil {
			return nil, fmt.Errorf("parsing %s.dockerignore: %w", prefix, err)
		}
	}
	dockerfile := cleanTreePath(b.Dockerfile)
	for p := range files {
		if neverInput(p) {
			continue
		}
		if p == dockerfile {
			out = append(out, p)
			continue
		}
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rel := strings.TrimPrefix(p, prefix)
		// Docker always sends the Dockerfile, whatever .dockerignore says; an auto-detected
		// one is any Dockerfile in the context.
		if dockerfile == "" && isDockerfileName(path.Base(rel)) {
			out = append(out, p)
			continue
		}
		if ignore != nil {
			skip, err := ignore.MatchesOrParentMatches(rel)
			if err != nil {
				return nil, fmt.Errorf("matching %s against .dockerignore: %w", rel, err)
			}
			if skip {
				continue
			}
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func readIgnorePatterns(repo *git.Repository, id plumbing.Hash) ([]string, error) {
	blob, err := repo.BlobObject(id)
	if err != nil {
		return nil, fmt.Errorf("reading .dockerignore: %w", err)
	}
	r, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("reading .dockerignore: %w", err)
	}
	defer r.Close()
	return ignorefile.ReadAll(r)
}

func neverInput(p string) bool {
	return p == ".git" || strings.HasPrefix(p, ".git/") || p == ".stagefreight" || strings.HasPrefix(p, ".stagefreight/")
}

func isDockerfileName(name string) bool {
	return name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile.") || strings.HasSuffix(name, ".Dockerfile")
}

func cleanTreePath(p string) string {
	p = path.Clean(strings.TrimPrefix(p, "./"))
	if p == "." || p == "/" {
		return ""
	}
	return strings.TrimPrefix(p, "/")
}
