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
package substrate

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
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
// packages (with versions), or already-present.
type Realized struct {
	Need     Need
	Backend  string
	Packages []PackageVersion
	Present  bool // already satisfied — nothing installed this run
}

// Realizer realizes capabilities into the build environment. This interface is the
// swappable seam: engines depend on it, never on apk/alpine. The bootstrap backend is
// apk; content-addressed / OCI-backed / remote realizers plug in behind it unchanged.
type Realizer interface {
	Realize(ctx context.Context, needs []Need) ([]Realized, error)
	Backend() string
}

// NewRealizer returns the realizer for the current build host. The apk backend
// (Alpine) is the bootstrap; on a host without apk it is a no-op that trusts the
// ambient environment (a dev workstation already has cc/cmake). cacheDir is the
// persistent package-cache root ("" disables caching) — keeping substrate offline-
// after-first alongside the toolchain and build caches.
func NewRealizer(cacheDir string) Realizer {
	if _, err := exec.LookPath("apk"); err == nil {
		return &apkRealizer{cacheDir: cacheDir}
	}
	return noopRealizer{}
}

type noopRealizer struct{}

func (noopRealizer) Backend() string                                     { return "none" }
func (noopRealizer) Realize(context.Context, []Need) ([]Realized, error) { return nil, nil }

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
