package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v5"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/release"
	"github.com/PrPlanIT/StageFreight/src/upstream"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

// autoReleaseUpstream is upstream.release: auto. After a successful default-branch run in
// track mode, when the claimed source's pins name a version upstream has released and no tag
// of ours names it yet, StageFreight tags the commit it just built with that version and
// pushes the tag. The release pipeline then runs every version guard and evidence probe like
// any hand-cut release. It never fails publish: a tag it cannot cut is reported.
func autoReleaseUpstream(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, rootDir string) {
	u := appCfg.Upstream
	if !u.AutoRelease() || !acceptedState(ciCtx) || ciCtx.SHA == "" {
		return
	}
	_, claimedID := u.Version.Mode()
	sources, err := upstream.Sources(u)
	if err != nil {
		return
	}
	for i, cs := range u.Sources {
		// Only the claimed source releases. Do NOT gate on cs.Pins/sync here: the version a
		// track release cuts can come from explicit pins, a history sync, OR — for an
		// image-base fork whose version lives in the Dockerfile FROM (discovered by the deps
		// engine, no upstream.pins entry) — the release the built commit contains.
		// autoReleaseTag owns that decision (readPins | newestContained) and returns a reason
		// when there is genuinely nothing; gating on pins here silently skips image-tracked
		// forks and never cuts their GA tag.
		if cs.ID != claimedID {
			continue
		}
		src := sources[i]
		sec := upstreamSection()
		sec.Row("%-14s%s", "tracking", src.String())
		tag, reason := autoReleaseTag(ctx, appCfg, rootDir, cs, src, ciCtx.SHA)
		if tag == "" {
			if reason != "" {
				sec.Row("%-14s%s", "release", "none cut — "+reason)
			} else {
				sec.Row("%-14s%s", "release", "none — already released")
			}
			sec.Close()
			return
		}
		msg := fmt.Sprintf("- Upstream %s released %s", src.ID, strings.TrimPrefix(tag, "v"))
		if err := release.CreateAnnotatedTag(rootDir, tag, ciCtx.SHA, msg); err != nil {
			sec.Row("%-14s%s %s", "release", output.StatusIcon("failed", output.UseColor()), tag+" not cut: "+err.Error())
			sec.Close()
			return
		}
		if err := release.PushTag(rootDir, "origin", tag, appCfg); err != nil {
			sec.Row("%-14s%s %s", "release", output.StatusIcon("failed", output.UseColor()), tag+" created but not pushed: "+err.Error())
			sec.Close()
			return
		}
		sec.Row("%-14s%s %s cut on %s", "release", output.StatusIcon("success", output.UseColor()), tag, ciCtx.SHA[:min(9, len(ciCtx.SHA))])
		sec.Row("%-14s%s", "", "version-as-claim: image proven to contain "+strings.TrimPrefix(tag, "v"))
		sec.Close()
		return
	}
}

// autoReleaseTag decides the tag to cut: "" with a reason when there is nothing to release.
// The version is: what the pins hold; for an image-base fork, the tag on the Dockerfile FROM
// that pins the tracked image; or — for a history fork synced onto upstream's releases — the
// newest upstream release whose commit the built commit contains.
func autoReleaseTag(ctx context.Context, appCfg *config.Config, rootDir string, cs config.UpstreamSource, src upstream.Source, built string) (tag, reason string) {
	l := listUpstream(ctx, src)
	if l.err != nil {
		return "", fmt.Sprintf("%s not listed: %v", src, l.err)
	}
	repo, err := gitstate.OpenRepo(rootDir)
	if err != nil {
		return "", err.Error()
	}
	var rel upstream.Release
	var current scheme.Version
	switch {
	case len(cs.Pins) > 0:
		pins, v, err := readPins(rootDir, cs, src.Select.Scheme)
		if err != nil {
			return "", err.Error()
		}
		r, ok := upstream.Find(l.releases, v)
		if !ok {
			return "", fmt.Sprintf("pins name %s, which %s has not released", pins[0].Value, src)
		}
		rel, current = r, v
	case src.Kind == config.UpstreamKindImage:
		// Image-base fork with no explicit pins: the version lives on the Dockerfile FROM the
		// deps engine bumps, and the evidence probe has already proven the built image matches
		// it. Read it back and release it — this is what makes an image-tracked fork cut its GA
		// tag without a redundant pins entry.
		v, raw, err := trackedImageVersion(rootDir, appCfg, src)
		if err != nil {
			return "", err.Error()
		}
		r, ok := upstream.Find(l.releases, v)
		if !ok {
			return "", fmt.Sprintf("the %s base is pinned to %s, which %s has not released", src.Location, raw, src)
		}
		rel, current = r, v
	default:
		r, ok := newestContained(repo, l.releases, built)
		if !ok {
			return "", fmt.Sprintf("the built commit contains no release of %s", src)
		}
		rel, current = r, r.Version
	}
	ours, _ := gitstate.ListTagsSorted(repo)
	vPrefix := true
	var newest string
	for _, t := range ours {
		v, ok := upstream.OurVersion(t, src.Select.Scheme)
		if !ok {
			continue
		}
		if v.Compare(current) == 0 {
			return "", "" // already released (a rebuild of it is the normal in-place rebuild)
		}
		if newest == "" {
			newest = t
		}
	}
	if newest != "" {
		vPrefix = strings.HasPrefix(newest, "v")
	}
	tag = strings.TrimPrefix(rel.Raw, "v")
	if vPrefix {
		tag = "v" + tag
	}
	return tag, ""
}

// newestContained is the newest release whose commit is an ancestor of (or is) built.
func newestContained(repo *git.Repository, releases []upstream.Release, built string) (upstream.Release, bool) {
	for _, r := range releases {
		if r.Commit == "" {
			continue
		}
		if ok, err := gitstate.IsAncestor(repo, r.Commit, built); err == nil && ok {
			return r, true
		}
	}
	return upstream.Release{}, false
}

// trackedImageVersion resolves the version an image-base fork is pinned to: the tag on the
// Dockerfile FROM line that references the tracked upstream image, across the repo's docker
// builds. It returns the parsed version and the raw tag. This is the version-as-claim source
// for a fork whose base lives in the FROM (no upstream.pins) — the deps engine bumps the tag,
// the evidence probe proves the built image matches it, and this reads it back to release.
func trackedImageVersion(rootDir string, appCfg *config.Config, src upstream.Source) (scheme.Version, string, error) {
	image := src.Location
	if image == "" {
		return nil, "", fmt.Errorf("%s has no image reference", src)
	}
	var tried []string
	for _, b := range appCfg.Builds {
		if b.Kind != "docker" {
			continue
		}
		df := b.Dockerfile
		if df == "" {
			df = "Dockerfile"
		}
		ctxDir := b.Context
		if ctxDir == "" {
			ctxDir = "."
		}
		path := filepath.Join(rootDir, ctxDir, df)
		tried = append(tried, path)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		raw := fromTag(string(data), image)
		if raw == "" {
			continue
		}
		// Parse the FROM tag through the SAME select regex/scheme the source's releases use,
		// so the "v" prefix, rewrites and scheme are handled identically — the resolved
		// version then compares cleanly against the listed releases.
		rels, _ := upstream.Releases([]string{raw}, src.Select)
		if len(rels) == 0 {
			return nil, raw, fmt.Errorf("the %s base is pinned to %q, which is not a recognized %s version", image, raw, src.Select.Scheme.Name())
		}
		return rels[0].Version, rels[0].Raw, nil
	}
	return nil, "", fmt.Errorf("no FROM pinning %s found (looked in %s)", image, strings.Join(tried, ", "))
}

// fromTag returns the tag on the first Dockerfile FROM line whose image (minus tag/digest)
// equals image, or "" when none matches. Handles `FROM [--platform=…] <ref>[:tag][@digest] [AS x]`.
func fromTag(dockerfile, image string) string {
	for _, line := range strings.Split(dockerfile, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !strings.EqualFold(f[0], "FROM") {
			continue
		}
		ref := ""
		for _, tok := range f[1:] {
			if strings.HasPrefix(tok, "--") { // --platform=… and friends
				continue
			}
			ref = tok
			break
		}
		if repo, tag := splitImageRef(ref); repo == image && tag != "" {
			return tag
		}
	}
	return ""
}

// splitImageRef splits an image reference into repo and tag, dropping any @digest. The tag is
// the part after the last ':' that follows the last '/', so a registry port (host:5000/img) is
// never mistaken for a tag.
func splitImageRef(ref string) (repo, tag string) {
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		ref = ref[:at]
	}
	slash := strings.LastIndexByte(ref, '/')
	colon := strings.LastIndexByte(ref, ':')
	if colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, ""
}
