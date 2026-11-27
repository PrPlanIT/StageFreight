package substrate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/atomicfile"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// capabilityPackages maps an abstract capability to Alpine apk packages. This is the
// ONLY place that knows distro package names — a Debian backend maps the SAME
// capabilities to build-essential / cmake / libssl-dev. Engines and manifests never
// see this table. A system package needs root; without it a registered realizer
// provides the capability (c-toolchain: a provisioned compiler).
var capabilityPackages = map[string][]string{
	"c-toolchain": {"build-base"}, // gcc, g++, make, musl-dev, binutils
	"cmake":       {"cmake"},
	"perl":        {"perl"},
	"pkg-config":  {"pkgconf"},
	"openssl":     {"openssl-dev", "openssl-libs-static"}, // dev = headers + .so; -static-pie musl links libssl.a/libcrypto.a
	"git":         {"git"},                                // build.rs version-stamping and tests that exec git
	"bash":        {"bash"},                               // rust's toolchain install.sh invokes bash (busybox sh is not enough)
	"openssh":     {"openssh-client-default"},             // ssh + ssh-keygen (openssh-client is virtual); tests that exec them
}

// capabilityProbe is a binary whose presence means the capability is already
// realized — making realization idempotent and a no-op when the tools exist (dev
// hosts, warm containers).
var capabilityProbe = map[string]string{
	"c-toolchain": "cc",
	"cmake":       "cmake",
	"perl":        "perl",
	"pkg-config":  "pkg-config",
	"git":         "git",
	"bash":        "bash",
	"openssh":     "ssh",
}

// relocatable marks the capabilities whose packages run from the substrate root (a
// usermode apk root under the cache) through its launchers; the others
// carry absolute /usr paths and install into the system, which needs root. cmake finds
// its modules beside its own binary; perl is pointed at its relocated library tree;
// pkg-config reads the root's .pc files with their prefix relocated to the root, and
// openssl-sys is pointed at the root's headers and libraries.
var relocatable = map[string]bool{"git": true, "bash": true, "cmake": true, "perl": true, "openssh": true,
	"pkg-config": true, "openssl": true}

// geteuid is the process's effective uid; a variable so tests can stand in for a
// non-root runner.
var geteuid = os.Geteuid

// apkCommand runs apk; a variable so tests can stand in for it.
var apkCommand = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "apk", args...).CombinedOutput()
}

// rootLockTimeout bounds the wait for another run's install into the same substrate root.
const rootLockTimeout = 10 * time.Minute

// realizeUserRoot installs the needs' packages into the substrate root — as any uid
// (apk's usermode when not root), signatures verified against the system's Alpine
// keys, package scripts not run — and returns what each capability runs with from there.
func realizeUserRoot(ctx context.Context, cacheDir string, needs []Need) ([]Realized, error) {
	root, err := userRoot(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("substrate: %w", err)
	}
	lock, err := paths.AcquireInstallLock(root, rootLockTimeout)
	if err != nil {
		return nil, fmt.Errorf("substrate: %w", err)
	}
	defer paths.ReleaseInstallLock(lock)

	var toInstall []string
	var realized []Realized
	seen := map[string]bool{}
	for _, n := range needs {
		probe := capabilityProbe[n.Capability]
		if probe != "" && rootHas(root, probe) {
			realized = append(realized, relocatedRealized(root, n, true))
			continue
		}
		for _, p := range capabilityPackages[n.Capability] {
			if !seen[p] {
				seen[p] = true
				toInstall = append(toInstall, p)
			}
		}
		realized = append(realized, relocatedRealized(root, n, false))
	}
	if len(toInstall) > 0 {
		sort.Strings(toInstall)
		args := []string{"add", "--quiet", "--no-progress", "--no-scripts", "--root", root,
			"--keys-dir", "/etc/apk/keys", "--repositories-file", "/etc/apk/repositories"}
		if geteuid() != 0 {
			args = append(args, "--usermode") // apk refuses it as root, and needs it otherwise
		}
		if _, err := os.Stat(filepath.Join(root, "lib", "apk", "db", "installed")); err != nil {
			args = append(args, "--initdb")
		}
		if dir := apkCacheDir(cacheDir); dir != "" {
			args = append(args, "--cache-dir", dir)
		}
		args = append(args, toInstall...)
		if out, err := apkCommand(ctx, args...); err != nil {
			// Only what was already there is realized; reporting the rest would claim
			// tools this run never staged.
			return presentOnly(realized), fmt.Errorf("substrate: apk add --root %s %v: %w\n%s", root, toInstall, err, strings.TrimSpace(string(out)))
		}
		versions := apkVersions(ctx, root, toInstall)
		for i := range realized {
			if realized[i].Present {
				continue
			}
			for _, p := range capabilityPackages[realized[i].Need.Capability] {
				realized[i].Packages = append(realized[i].Packages, PackageVersion{Name: p, Version: versions[p]})
			}
		}
	}
	if err := writeRootWrappers(root); err != nil {
		return realized, fmt.Errorf("substrate: %w", err)
	}
	return realized, nil
}

// alpineRelease is the host's Alpine release, which keys the substrate root: packages
// are linked against the host's musl and must match it.
var alpineRelease = func() string {
	b, err := os.ReadFile("/etc/alpine-release")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

// userRoot is the usermode apk root for this Alpine release under cacheDir, or a fresh
// temporary root when there is no persistent cache.
func userRoot(cacheDir string) (string, error) {
	if cacheDir == "" {
		return os.MkdirTemp("", "stagefreight-substrate-")
	}
	root := filepath.Join(cacheDir, "root", alpineRelease())
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("substrate root %s: %w", root, err)
	}
	return root, nil
}

// wrapDir holds a launcher for each binary in a substrate root.
const wrapDir = "wrap"

// writeRootWrappers writes a launcher in <root>/wrap for every executable in the root's
// bin directories. A launcher finds the root from its own path and puts the root's
// libraries on LD_LIBRARY_PATH for that binary's process tree only.
func writeRootWrappers(root string) error {
	dir := filepath.Join(root, wrapDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, bin := range []string{"usr/bin", "bin"} {
		entries, err := os.ReadDir(filepath.Join(root, bin))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !isExecutable(filepath.Join(root, bin, e.Name())) {
				continue
			}
			body := "#!/bin/sh\n" +
				"r=\"${0%/*}/..\"\n" +
				"export LD_LIBRARY_PATH=\"$r/usr/lib:$r/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}\"\n" +
				"exec \"$r/" + bin + "/" + e.Name() + "\" \"$@\"\n"
			if err := atomicfile.WriteFileIfChanged(filepath.Join(dir, e.Name()), []byte(body), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

// relocatedRealized is a capability realized in the substrate root: its launchers on
// PATH, and the env its binaries need to run from there.
func relocatedRealized(root string, n Need, present bool) Realized {
	r := Realized{
		Need: n, Backend: "apk-alpine (usermode)", Present: present,
		PathDirs: []string{filepath.Join(root, wrapDir)},
	}
	switch n.Capability {
	case "git":
		r.Env = map[string]string{
			"GIT_EXEC_PATH":    filepath.Join(root, "usr", "libexec", "git-core"),
			"GIT_TEMPLATE_DIR": filepath.Join(root, "usr", "share", "git-core", "templates"),
		}
	case "perl":
		// perl's @INC is compiled in as /usr/...; the relocated tree is found through
		// PERL5LIB, in the order Alpine's perl searches it.
		var lib []string
		for _, d := range []string{"usr/local/lib/perl5/site_perl", "usr/local/share/perl5/site_perl",
			"usr/lib/perl5/vendor_perl", "usr/share/perl5/vendor_perl", "usr/lib/perl5/core_perl", "usr/share/perl5/core_perl"} {
			lib = append(lib, filepath.Join(root, d))
		}
		r.Env = map[string]string{"PERL5LIB": strings.Join(lib, ":")}
	case "pkg-config":
		// The root's .pc files say prefix=/usr; relocation derives each prefix from where
		// its .pc file sits, so the root's packages resolve into the root while the
		// host's own .pc files (found through pkgconf's built-in path) stay as they are.
		r.Env = map[string]string{
			"PKG_CONFIG_PATH": filepath.Join(root, "usr", "lib", "pkgconfig") + ":" +
				filepath.Join(root, "usr", "share", "pkgconfig"),
			"PKG_CONFIG_RELOCATE_PATHS": "1",
		}
	case "openssl":
		// openssl-sys reads OPENSSL_DIR's include/ and lib/ before asking pkg-config: the
		// same headers and libraries a system install would give it.
		r.Env = map[string]string{"OPENSSL_DIR": filepath.Join(root, "usr")}
	}
	return r
}

// realizeSystem installs the needs' packages into the system with apk, which takes
// root; a non-root runner gets an error naming what is missing and why. Nothing is
// returned as realized unless apk installed it.
func realizeSystem(ctx context.Context, cacheDir string, needs []Need) ([]Realized, error) {
	var toInstall []string
	var realized []Realized
	seen := map[string]bool{}
	for _, n := range needs {
		for _, p := range capabilityPackages[n.Capability] {
			if !seen[p] {
				seen[p] = true
				toInstall = append(toInstall, p)
			}
		}
		realized = append(realized, Realized{Need: n, Backend: "apk-alpine"})
	}
	sort.Strings(toInstall)
	if uid := geteuid(); uid != 0 {
		return nil, fmt.Errorf("substrate: %s needs %s, which cannot be installed without root (this runner runs as uid %d): "+
			"run it on a runner that runs as root, or in a job image that ships %s",
			needSources(needs), missingCapabilities(realized), uid, strings.Join(toInstall, " "))
	}
	args := []string{"add", "--no-progress"}
	if dir := apkCacheDir(cacheDir); dir != "" {
		args = append(args, "--cache-dir", dir)
	}
	args = append(args, toInstall...)
	if out, err := apkCommand(ctx, args...); err != nil {
		return nil, fmt.Errorf("substrate: apk add %v: %w\n%s", toInstall, err, strings.TrimSpace(string(out)))
	}
	versions := apkVersions(ctx, "", toInstall)
	for i := range realized {
		for _, p := range capabilityPackages[realized[i].Need.Capability] {
			realized[i].Packages = append(realized[i].Packages, PackageVersion{Name: p, Version: versions[p]})
		}
	}
	return realized, nil
}

// presentOnly keeps the capabilities that were already realized before an install.
func presentOnly(realized []Realized) []Realized {
	var out []Realized
	for _, r := range realized {
		if r.Present {
			out = append(out, r)
		}
	}
	return out
}

// apkCacheDir is the package download cache under cacheDir, created; "" when there is
// no cache or it can't be created.
func apkCacheDir(cacheDir string) string {
	if cacheDir == "" {
		return ""
	}
	dir := filepath.Join(cacheDir, "apk-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// rootHas reports whether the substrate root holds an executable named bin.
func rootHas(root, bin string) bool {
	return isExecutable(filepath.Join(root, "usr", "bin", bin)) || isExecutable(filepath.Join(root, "bin", bin))
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// missingCapabilities names the capabilities that were not already present.
func missingCapabilities(realized []Realized) string {
	var caps []string
	for _, r := range realized {
		if !r.Present {
			caps = append(caps, r.Need.Capability)
		}
	}
	return strings.Join(caps, ", ")
}

// needSources names who asked for the needs ("install.sh", "cargo", …), deduplicated.
func needSources(needs []Need) string {
	seen := map[string]bool{}
	var out []string
	for _, n := range needs {
		if n.Source != "" && !seen[n.Source] {
			seen[n.Source] = true
			out = append(out, n.Source)
		}
	}
	if len(out) == 0 {
		return "the build"
	}
	return strings.Join(out, ", ")
}

// apkVersions returns best-effort installed versions, e.g. git → "2.54.0-r0", from the
// system database or, with root set, the usermode root's. `apk list --installed <pkg>`
// yields "<name>-<version> <arch> {...}".
func apkVersions(ctx context.Context, root string, pkgs []string) map[string]string {
	out := make(map[string]string, len(pkgs))
	for _, p := range pkgs {
		args := []string{"list", "--installed"}
		if root != "" {
			args = append(args, "--root", root)
		}
		b, err := apkCommand(ctx, append(args, p)...)
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
