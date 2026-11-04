// Package substrate realizes the native BUILD ENVIRONMENT a build needs — C
// toolchains, build helpers, sysroots — as a peer of the toolchain layer, under the
// same ownership discipline: inferred (engines emit CAPABILITIES, never packages),
// realized by a backend (which alone knows distro/package semantics), cached on the
// persistent mount, and recorded as explicit provenance.
//
// Invariants (load-bearing — do not erode):
//   - engines infer CAPABILITIES, not packages
//   - substrate owns realization
//   - the backend owns distro/package semantics
//   - operators declare intent, not incidental mechanics
//   - apk is the bootstrap TRANSPORT, not the architecture
//   - a realized substrate is EXPLICIT STATE (recorded), not ambient host mutation
//   - realization needs no root: the image stays minimal and any uid can build
package substrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Need is an inferred capability a build requires from its environment, carrying the
// reason and source so realization is explainable, provenance-able, and policy-able.
// A capability is abstract ("c-toolchain"), never a package ("build-base").
type Need struct {
	Capability string // "c-toolchain", "cmake", "perl", "pkg-config", "openssl"
	Reason     string // why: "rust-build-script-linking", "crate-native-build"
	Source     string // who asked: "cargo", "aws-lc-sys"
}

// PackageVersion is a realized package + its version — the explicit state that
// becomes build-identity provenance (a libc/cmake/perl version can matter for
// reproducibility and supply-chain response).
type PackageVersion struct {
	Name    string
	Version string
}

// Realized is the explicit outcome of realizing one Need: which backend, which
// packages (with versions), or already-present — and the environment a consumer
// applies (ApplyEnv) to use what was realized outside the system paths.
type Realized struct {
	Need     Need
	Backend  string
	Packages []PackageVersion
	Present  bool // already satisfied — nothing installed this run

	PathDirs []string          // prepended to PATH
	Env      map[string]string // set as given
}

// Realizer realizes capabilities into the build environment. This interface is the
// swappable seam: engines depend on it, never on apk/alpine.
type Realizer interface {
	Realize(ctx context.Context, needs []Need) ([]Realized, error)
	Backend() string
}

// CapabilityRealizer realizes one capability by its own means (e.g. a provisioned
// toolchain), without root, where the package backend can't install it, provisioning
// for the repo at rootDir. It returns ErrUnavailable when it has nothing for this host.
type CapabilityRealizer func(ctx context.Context, rootDir string, n Need) (Realized, error)

// ErrUnavailable is a CapabilityRealizer's answer when it can't serve this host.
var ErrUnavailable = errors.New("not available on this host")

var capabilityRealizers = struct {
	sync.RWMutex
	m map[string]CapabilityRealizer
}{m: map[string]CapabilityRealizer{}}

// RegisterCapability makes fn the rootless realizer for capability: used when the
// package backend can't install it (no apk, or no root for a system package).
func RegisterCapability(capability string, fn CapabilityRealizer) {
	capabilityRealizers.Lock()
	defer capabilityRealizers.Unlock()
	capabilityRealizers.m[capability] = fn
}

func registeredRealizer(capability string) CapabilityRealizer {
	capabilityRealizers.RLock()
	defer capabilityRealizers.RUnlock()
	return capabilityRealizers.m[capability]
}

// NewRealizer returns the realizer for the current build host. Registered capability
// realizers run on any host; package capabilities use apk where it exists, and on a
// host without apk the ambient environment is trusted (a dev workstation has its own
// git and cmake). rootDir is the repo the build runs for; cacheDir is the persistent
// substrate root ("" disables persistence).
func NewRealizer(rootDir, cacheDir string) Realizer {
	_, err := exec.LookPath("apk")
	return &realizer{rootDir: rootDir, cacheDir: cacheDir, apk: err == nil}
}

type realizer struct {
	rootDir  string
	cacheDir string
	apk      bool
}

func (r *realizer) Backend() string {
	if r.apk {
		return "apk-alpine"
	}
	return "none"
}

// Realize realizes each need: a tool already on PATH; else a package — into the
// substrate root (any uid) where it runs from there, into the system as root; else a
// registered rootless realizer; else, with no apk at all, the ambient environment.
func (r *realizer) Realize(ctx context.Context, needs []Need) ([]Realized, error) {
	var realized []Realized
	var userNeeds, systemNeeds []Need
	for _, n := range needs {
		if _, ok := capabilityPackages[n.Capability]; !ok {
			return realized, fmt.Errorf("substrate: unknown capability %q (from %s)", n.Capability, n.Source)
		}
		if probe, has := capabilityProbe[n.Capability]; has {
			if _, err := exec.LookPath(probe); err == nil {
				realized = append(realized, Realized{Need: n, Backend: "host", Present: true})
				continue
			}
		}
		switch {
		case r.apk && relocatable[n.Capability]:
			userNeeds = append(userNeeds, n)
			continue
		case r.apk && geteuid() == 0:
			systemNeeds = append(systemNeeds, n)
			continue
		}
		if fn := registeredRealizer(n.Capability); fn != nil {
			rz, err := fn(ctx, r.rootDir, n)
			if err == nil {
				realized = append(realized, rz)
				continue
			}
			if !errors.Is(err, ErrUnavailable) {
				return realized, fmt.Errorf("substrate: %s for %s: %w", n.Capability, n.Source, err)
			}
		}
		if r.apk {
			systemNeeds = append(systemNeeds, n) // refused below with what is missing
		}
	}
	if len(userNeeds) > 0 {
		rz, err := realizeUserRoot(ctx, r.cacheDir, userNeeds)
		realized = append(realized, rz...)
		if err != nil {
			return realized, err
		}
	}
	if len(systemNeeds) > 0 {
		rz, err := realizeSystem(ctx, r.cacheDir, systemNeeds)
		realized = append(realized, rz...)
		if err != nil {
			return realized, err
		}
	}
	return realized, nil
}

// ApplyEnv returns a copy of env with every realized capability's PATH prefixes and
// variables applied. PATH absent from env is read from the process environment before
// prefixing, so an empty base still finds the system PATH.
func ApplyEnv(env []string, realized []Realized) []string {
	env = append([]string(nil), env...)
	var paths []string
	vars := map[string]string{}
	for _, r := range realized {
		for _, d := range r.PathDirs {
			if !contains(paths, d) {
				paths = append(paths, d)
			}
		}
		for k, v := range r.Env {
			vars[k] = v
		}
	}
	if len(paths) > 0 {
		env = prependList(env, "PATH", paths)
	}
	for k, v := range vars {
		env = setVar(env, k, v)
	}
	return env
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func prependList(env []string, key string, dirs []string) []string {
	cur, found := lookupVar(env, key)
	if !found {
		cur = os.Getenv(key)
	}
	val := strings.Join(dirs, string(os.PathListSeparator))
	if cur != "" {
		val += string(os.PathListSeparator) + cur
	}
	return setVar(env, key, val)
}

func lookupVar(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix), true
		}
	}
	return "", false
}

func setVar(env []string, key, val string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}

// Report prints the realized substrate as provenance — the same discipline as
// toolchain.Report. Visible, explicit state, not silent host mutation.
func Report(w io.Writer, realized []Realized) {
	for _, r := range realized {
		var pkgs []string
		for _, p := range r.Packages {
			if p.Version != "" {
				pkgs = append(pkgs, p.Name+"-"+p.Version)
			} else {
				pkgs = append(pkgs, p.Name)
			}
		}
		state := "realized"
		if r.Present {
			state = "present"
		}
		detail := strings.Join(pkgs, " ")
		if detail == "" {
			detail = r.Backend
		}
		fmt.Fprintf(w, "    substrate   %-14s %-9s %s  (%s)\n", r.Need.Capability, state, detail, r.Need.Reason)
	}
}
