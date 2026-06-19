package substrate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// capabilityPackages maps an abstract capability to Alpine apk packages. This is the
// ONLY place that knows distro package names — a Debian backend maps the SAME
// capabilities to build-essential / cmake / libssl-dev. Engines and manifests never
// see this table.
var capabilityPackages = map[string][]string{
	"c-toolchain": {"build-base"}, // gcc, g++, make, musl-dev, binutils
	"cmake":       {"cmake"},
	"perl":        {"perl"},
	"pkg-config":  {"pkgconf"},
	"openssl":     {"openssl-dev"},
	"git":         {"git"}, // build.rs version-stamping (the SF image is otherwise git-less)
}

// capabilityProbe is a binary whose presence means the capability is already
// realized — making realization idempotent and a no-op when the tools exist (dev
// hosts, warm containers). A capability without a probe is always (re)realized;
// apk add is itself idempotent.
var capabilityProbe = map[string]string{
	"c-toolchain": "cc",
	"cmake":       "cmake",
	"perl":        "perl",
	"pkg-config":  "pkg-config",
	"git":         "git",
}

// apkRealizer is the bootstrap backend: Alpine apk with a persistent package cache.
type apkRealizer struct {
	cacheDir string
}

func (r *apkRealizer) Backend() string { return "apk-alpine" }

func (r *apkRealizer) Realize(ctx context.Context, needs []Need) ([]Realized, error) {
	var realized []Realized
	var toInstall []string
	seen := map[string]bool{}

	for _, n := range needs {
		pkgs, ok := capabilityPackages[n.Capability]
		if !ok {
			return realized, fmt.Errorf("substrate: unknown capability %q (from %s)", n.Capability, n.Source)
		}
		if probe, has := capabilityProbe[n.Capability]; has {
			if _, err := exec.LookPath(probe); err == nil {
				realized = append(realized, Realized{Need: n, Backend: r.Backend(), Present: true})
				continue
			}
		}
		for _, p := range pkgs {
			if !seen[p] {
				seen[p] = true
				toInstall = append(toInstall, p)
			}
		}
		realized = append(realized, Realized{Need: n, Backend: r.Backend()})
	}

	if len(toInstall) == 0 {
		return realized, nil
	}
	sort.Strings(toInstall)

	args := []string{"add", "--no-progress"}
	if r.cacheDir != "" {
		if err := os.MkdirAll(r.cacheDir, 0o755); err == nil {
			args = append(args, "--cache-dir", r.cacheDir)
		}
	}
	args = append(args, toInstall...)
	if out, err := exec.CommandContext(ctx, "apk", args...).CombinedOutput(); err != nil {
		return realized, fmt.Errorf("substrate: apk add %v: %w\n%s", toInstall, err, strings.TrimSpace(string(out)))
	}

	// Capture installed versions for provenance (best-effort).
	versions := apkVersions(ctx, toInstall)
	for i := range realized {
		if realized[i].Present {
			continue
		}
		for _, p := range capabilityPackages[realized[i].Need.Capability] {
			realized[i].Packages = append(realized[i].Packages, PackageVersion{Name: p, Version: versions[p]})
		}
	}
	return realized, nil
}

// apkVersions returns best-effort installed versions, e.g. build-base → "0.5-r3".
// `apk list --installed <pkg>` yields "<name>-<version> <arch> {...}"; since we know
// the package name, the version is the first field with the "<name>-" prefix removed.
func apkVersions(ctx context.Context, pkgs []string) map[string]string {
	out := make(map[string]string, len(pkgs))
	for _, p := range pkgs {
		b, err := exec.CommandContext(ctx, "apk", "list", "--installed", p).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if fields := strings.Fields(line); len(fields) > 0 && strings.HasPrefix(fields[0], p+"-") {
				out[p] = strings.TrimPrefix(fields[0], p+"-")
				break
			}
		}
	}
	return out
}
