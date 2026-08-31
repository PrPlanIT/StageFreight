package discovery

import (
	"context"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// checkDockerfile is the top-level dispatcher for Dockerfile freshness.
// It parses the file once, then fans out to sub-checkers for images,
// tools, apk, apt, and pip.
func (m *Resolver) checkDockerfile(ctx context.Context, file lint.FileInfo) ([]supplychain.Dependency, error) {
	dfInfo, err := parseDockerfileForFreshness(file.AbsPath)
	if err != nil {
		return nil, err
	}

	var deps []supplychain.Dependency

	// Base image freshness
	if m.cfg.SourceEnabled(supplychain.EcosystemDockerImage) {
		deps = append(deps, m.checkImages(ctx, file, dfInfo)...)
	}

	// Pinned tool versions (ENV *_VERSION + GitHub releases)
	deps = append(deps, m.checkToolsFromDockerfile(ctx, file, dfInfo)...)

	// Alpine APK packages
	if m.cfg.SourceEnabled(supplychain.EcosystemAlpineAPK) && len(dfInfo.ApkPackages) > 0 {
		alpineVer := detectAlpineVersion(dfInfo)
		if alpineVer != "" {
			apkDeps := m.checkAPK(ctx, file, dfInfo.ApkPackages, alpineVer)
			deps = append(deps, apkDeps...)
		}
	}

	// Debian/Ubuntu APT packages
	if m.cfg.SourceEnabled(supplychain.EcosystemDebianAPT) && len(dfInfo.AptPackages) > 0 {
		distro, codename := detectDebianDistro(dfInfo.Stages)
		if distro != "" && codename != "" {
			aptDeps := m.checkAPT(ctx, file, dfInfo.AptPackages, distro, codename)
			deps = append(deps, aptDeps...)
		}
	}

	// pip packages found in RUN pip install
	if m.cfg.SourceEnabled(supplychain.EcosystemPip) && len(dfInfo.PipPackages) > 0 {
		pipDeps := m.resolvePipPackages(ctx, file, dfInfo.PipPackages)
		deps = append(deps, pipDeps...)
	}

	return deps, nil
}

// detectAlpineVersion extracts the Alpine version from base images.
// e.g. "alpine:3.22" → "3.22", "golang:1.25-alpine3.22" → "3.22". An interpolated base
// (FROM alpine:${ALPINE_VERSION}) is resolved via the Dockerfile's ARG/ENV values first,
// so apk resolution is not silently skipped when the version lives in an ARG.
func detectAlpineVersion(info *supplychain.DockerFreshnessInfo) string {
	for _, s := range info.Stages {
		image := s.Image
		if resolved, _, _, ok := resolveImageRef(image, info); ok {
			image = resolved
		}
		// Strip a digest pin so the tag is recoverable (image:tag@sha256:… → image:tag).
		ref, _ := SplitImageDigest(image)
		image, tag := SplitImageTag(ref)
		if tag == "" {
			continue
		}
		ns, repo := SplitImageNamespace(image)
		if (ns == "library" || ns == "") && repo == "alpine" {
			dt := version.DecomposeTag(tag)
			if dt.Version != nil {
				return tag
			}
		}
		// Suffix-based (e.g. "1.25-alpine3.22")
		dt := version.DecomposeTag(tag)
		if dt.Suffix != "" {
			// Look for "alpine" prefix in suffix, e.g. "alpine3.22"
			if len(dt.Suffix) > 6 && dt.Suffix[:6] == "alpine" {
				ver := dt.Suffix[6:]
				if v := version.ParseVersion(ver); v != nil {
					return ver
				}
			}
			// Just "alpine" with no version — use latest stable
			if dt.Suffix == "alpine" {
				return "" // can't determine version
			}
		}
	}
	return ""
}

// detectDebianDistro detects Debian/Ubuntu from base images.
// Returns (distro, codename) e.g. ("debian", "bookworm") or ("ubuntu", "noble").
func detectDebianDistro(stages []supplychain.StageInfo) (string, string) {
	for _, s := range stages {
		image, tag := SplitImageTag(s.Image)
		if tag == "" {
			continue
		}
		_, repo := SplitImageNamespace(image)

		switch repo {
		case "debian":
			return "debian", suiteFromTag(tag)
		case "ubuntu":
			return "ubuntu", suiteFromTag(tag)
		}

		// Check suffix for debian/ubuntu base
		dt := version.DecomposeTag(tag)
		if dt.Suffix != "" {
			for _, d := range []string{"bookworm", "bullseye", "buster", "trixie"} {
				if dt.Suffix == d {
					return "debian", d
				}
			}
			for _, u := range []string{"noble", "jammy", "focal", "mantic", "lunar"} {
				if dt.Suffix == u {
					return "ubuntu", u
				}
			}
		}
	}
	return "", ""
}

// suiteFromTag reduces a base-image tag to the archive suite its packages come from.
// Debian and Ubuntu images publish variant tags — trixie-slim, bookworm-backports —
// but the archive is indexed by the suite alone, so passing the raw tag builds a
// dists/<tag>/ URL that 404s. The fetch then fails silently and every package in the
// image goes untracked, which is indistinguishable from "nothing to update".
//
// Only the leading suite is kept; a bare tag passes through unchanged.
func suiteFromTag(tag string) string {
	if i := strings.IndexByte(tag, '-'); i > 0 {
		return tag[:i]
	}
	return tag
}
