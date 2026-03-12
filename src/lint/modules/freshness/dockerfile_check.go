package freshness

import (
	"context"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// checkDockerfile is the top-level dispatcher for Dockerfile freshness.
// It parses the file once, then fans out to sub-checkers for images,
// tools, apk, apt, and pip.
func (m *freshnessModule) checkDockerfile(ctx context.Context, file lint.FileInfo) ([]Dependency, error) {
	dfInfo, err := parseDockerfileForFreshness(file.AbsPath)
	if err != nil {
		return nil, err
	}

	var deps []Dependency

	// Base image freshness
	if m.cfg.sourceEnabled(EcosystemDockerImage) {
		deps = append(deps, m.checkImages(ctx, file, dfInfo.Stages)...)
	}

	// Pinned tool versions (ENV *_VERSION + GitHub releases)
	deps = append(deps, m.checkToolsFromDockerfile(ctx, file, dfInfo)...)

	// Alpine APK packages
	if m.cfg.sourceEnabled(EcosystemAlpineAPK) && len(dfInfo.ApkPackages) > 0 {
		alpineVer := detectAlpineVersion(dfInfo.Stages)
		if alpineVer != "" {
			apkDeps := m.checkAPK(ctx, file, dfInfo.ApkPackages, alpineVer)
			deps = append(deps, apkDeps...)
		}
	}

	// Debian/Ubuntu APT packages
	if m.cfg.sourceEnabled(EcosystemDebianAPT) && len(dfInfo.AptPackages) > 0 {
		distro, codename := detectDebianDistro(dfInfo.Stages)
		if distro != "" && codename != "" {
			aptDeps := m.checkAPT(ctx, file, dfInfo.AptPackages, distro, codename)
			deps = append(deps, aptDeps...)
		}
	}

	// pip packages found in RUN pip install
	if m.cfg.sourceEnabled(EcosystemPip) && len(dfInfo.PipPackages) > 0 {
		pipDeps := m.resolvePipPackages(ctx, file, dfInfo.PipPackages)
		deps = append(deps, pipDeps...)
	}

	return deps, nil
}

// detectAlpineVersion extracts the Alpine version from base images.
// e.g. "alpine:3.22" → "3.22", "golang:1.25-alpine3.22" → "3.22"
func detectAlpineVersion(stages []stageInfo) string {
	for _, s := range stages {
		_, tag := splitImageTag(s.Image)
		if tag == "" {
			continue
		}
		// Direct alpine image
		image, _ := splitImageTag(s.Image)
		ns, repo := splitImageNamespace(image)
		if (ns == "library" || ns == "") && repo == "alpine" {
			dt := decomposeTag(tag)
			if dt.Version != nil {
				return tag
			}
		}
		// Suffix-based (e.g. "1.25-alpine3.22")
		dt := decomposeTag(tag)
		if dt.Suffix != "" {
			// Look for "alpine" prefix in suffix, e.g. "alpine3.22"
			if len(dt.Suffix) > 6 && dt.Suffix[:6] == "alpine" {
				ver := dt.Suffix[6:]
				if v := parseVersion(ver); v != nil {
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
func detectDebianDistro(stages []stageInfo) (string, string) {
	for _, s := range stages {
		image, tag := splitImageTag(s.Image)
		if tag == "" {
			continue
		}
		_, repo := splitImageNamespace(image)

		switch repo {
		case "debian":
			return "debian", tag
		case "ubuntu":
			return "ubuntu", tag
		}

		// Check suffix for debian/ubuntu base
		dt := decomposeTag(tag)
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
