package version

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"runtime"
)

// These variables are injected at build time via -ldflags.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// ReplayGuard names the convergence/replay correctness guarantee compiled into
// THIS binary. It is runtime-provable provenance: the replay change-set
// equivalence gate (src/commit) and this marker ship in the same build, so a
// stale binary built before the gate reports no/older marker. That closes the
// "binary claimed a lineage it did not possess" gap — a forged or absent version
// stamp can no longer hide a missing guard. Bump when the replay invariant changes.
const ReplayGuard = "change-set-equivalence/v1"

// String returns the one-line version string.
func String() string {
	return fmt.Sprintf("stagefreight %s (%s, %s)", Version, Commit, BuildDate)
}

// SelfHash returns the SHA-256 of the running executable — authoritative runtime
// identity that does NOT depend on the (forgeable, ldflags-set) version stamp.
// Two binaries with the same stamp but different code have different SelfHash.
func SelfHash() string {
	path, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	f, err := os.Open(path)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Verbose returns multi-line provenance: the build stamp plus runtime-derived
// identity (Go version, executable SHA-256) and the compiled-in replay-guard
// capability — so "which binary is actually running, and does it carry the
// guard?" is answerable without trusting the embedded stamp alone.
func Verbose() string {
	return fmt.Sprintf(
		"stagefreight %s\n"+
			"  commit:        %s\n"+
			"  built:         %s\n"+
			"  go:            %s\n"+
			"  binary sha256: %s\n"+
			"  replay-guard:  %s",
		Version, Commit, BuildDate, runtime.Version(), SelfHash(), ReplayGuard,
	)
}
