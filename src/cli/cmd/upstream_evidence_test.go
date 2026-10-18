package cmd

import (
	"regexp"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/upstream"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

func mkCommit(t *testing.T, repo *git.Repository, msg string, parents ...plumbing.Hash) plumbing.Hash {
	t.Helper()
	wt, _ := repo.Worktree()
	sig := &object.Signature{Name: "t", Email: "t@t", When: time.Unix(1700000000, 0)}
	h, err := wt.Commit(msg, &git.CommitOptions{Author: sig, AllowEmptyCommits: true, Parents: parents})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAncestryVerdict(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	upstreamRelease := mkCommit(t, repo, "upstream v4.19.5")
	ours := mkCommit(t, repo, "our patch on top", upstreamRelease)
	_ = ours // HEAD
	other, _ := git.PlainInit(t.TempDir(), false)
	foreign := mkCommit(t, other, "upstream v4.19.6, never merged")

	sem, _ := scheme.Get(scheme.Semver)
	claim := upstream.Claim{Mode: config.UpstreamVersionTrack, Source: upstream.Source{ID: "zitadel", Select: upstream.Selection{Scheme: sem}}}
	releases := []upstream.Release{
		{Tag: "v4.19.6", Version: mustV(t, sem, "4.19.6"), Commit: foreign.String()},
		{Tag: "v4.19.5", Version: mustV(t, sem, "4.19.5"), Commit: upstreamRelease.String()},
	}
	if r := ancestryVerdict(dir, claim, releases, "v4.19.5"); r != "" {
		t.Fatalf("our release built on upstream's v4.19.5 must pass: %s", r)
	}
	if r := ancestryVerdict(dir, claim, releases, "v4.19.6"); !strings.Contains(r, "not in our history") {
		t.Fatalf("a tag naming upstream's release on code without it must be refused: %q", r)
	}

	// Our history has the commit, but HEAD is an orphan line that does not descend from it.
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/rewritten")); err != nil {
		t.Fatal(err)
	}
	mkCommit(t, repo, "rewritten history")
	if r := ancestryVerdict(dir, claim, releases, "v4.19.5"); !strings.Contains(r, "not an ancestor") {
		t.Fatalf("HEAD off upstream's line must be refused: %q", r)
	}
}

func mustV(t *testing.T, s scheme.Scheme, v string) scheme.Version {
	t.Helper()
	out, err := s.Parse(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEvidenceGate(t *testing.T) {
	img := empty.Image
	cf, _ := img.ConfigFile()
	cf = cf.DeepCopy()
	cf.Config.Labels = map[string]string{"org.opencontainers.image.version": "2.21.5"}
	img, _ = mutate.ConfigFile(img, cf)
	dir := t.TempDir()
	p, err := layout.Write(dir, empty.Index)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AppendImage(img); err != nil {
		t.Fatal(err)
	}
	digest, _ := img.Digest()

	cfg := &config.Config{Upstream: config.UpstreamConfig{
		Sources: config.OrderedUpstreamSources{{ID: "core", Git: "https://github.com/ansible/ansible"}},
		Version: config.UpstreamVersion{Track: "core"},
		Evidence: config.OrderedUpstreamEvidence{
			{ID: "image", Probe: config.UpstreamProbe{Label: "org.opencontainers.image.version"}},
			{ID: "tolerant", Probe: config.UpstreamProbe{Label: "org.opencontainers.image.version"}, OnMismatch: "report", Reason: "metadata trails the tag"},
		},
	}}

	t.Setenv("SF_CI_TAG", "")
	if newEvidenceGate(cfg) != nil {
		t.Fatal("a dev build claims no version; nothing to prove")
	}

	t.Setenv("SF_CI_TAG", "v2.21.5-2")
	g := newEvidenceGate(cfg)
	if r := g.check("image", dir, digest.String()); r != "" {
		t.Fatalf("the image contains the claimed version: %s", r)
	}
	if r := g.check("unprobed", dir, digest.String()); r != "" || g.checked != 1 {
		t.Fatalf("a build without evidence is not gated here (validation requires it): %q", r)
	}

	t.Setenv("SF_CI_TAG", "v2.21.6")
	g = newEvidenceGate(cfg)
	if r := g.check("image", dir, digest.String()); !regexp.MustCompile(`contains 2\.21\.5, but release v2\.21\.6 claims 2\.21\.6`).MatchString(r) {
		t.Fatalf("a mislabelled image must be refused: %q", r)
	}
	if r := g.check("tolerant", dir, digest.String()); r != "" || !g.reported {
		t.Fatalf("on_mismatch: report distributes and reports: %q", r)
	}
}
