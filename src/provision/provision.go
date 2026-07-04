// Package provision is StageFreight's shared environment-realization surface: the
// single, honest way every subsystem communicates what tools and native capabilities
// it pulled in, where from, and how (if at all) it verified them.
//
// It is a trust LEDGER, not decoration. One fixed-column table — tool, version, via,
// verified, purpose — rendered in its own box, separate from whatever the subsystem
// went on to DO with those tools. Columns never change meaning row-to-row; a cell is
// its column's datum or blank.
//
//	── Staged Tools ───────────────────────────────────
//	│   tool            version    via        verified    purpose
//	│   go              1.26.1     go.dev     checksum
//	│   cargo-llvm-cov  0.8.7      github     tofu        coverage instrumentation
//	│   build-base      0.5-r4     apk        delegated   C compiler for the race detector (cgo)
//	└───────────────────────────────────────────────────────
package provision

import (
	"context"
	"io"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/substrate"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// Entry is one realized dependency — a single row of the trust ledger.
type Entry struct {
	Tool     string // what was realized (go, cargo-llvm-cov, build-base, git)
	Version  string // exact resolved version
	Via      string // the resolution path we took (github, go.dev, rust-lang, apk, https)
	Verified string // how WE verified it (checksum|tofu|pinned|signed|delegated); "" = unknown
	Purpose  string // why it was pulled; blank for the base toolchain
}

// Verified vocabulary. checksum/tofu/pinned/signed are things WE did; delegated means
// we verified nothing ourselves and rely on the package manager's own chain (apk).
const (
	VerifiedDelegated = "delegated"
)

// FromToolchain maps a resolved toolchain into a ledger row. verified comes straight
// from Result.Trust (already pinned|checksum|tofu). purpose is blank for a base
// toolchain, or a short reason when pulled for something specific (e.g. coverage).
func FromToolchain(r toolchain.Result, purpose string) Entry {
	return Entry{Tool: r.Tool, Version: r.Version, Via: via(r.SourceURL), Verified: r.Trust, Purpose: purpose}
}

// FromSubstrate maps realized native capabilities into ledger rows — one per installed
// package. Substrate comes from apk (via=apk), which we do not verify ourselves
// (verified=delegated); the honest label makes that non-verification visible.
func FromSubstrate(realized []substrate.Realized) []Entry {
	var out []Entry
	for _, rz := range realized {
		purpose := humanizeReason(rz.Need.Reason)
		if len(rz.Packages) == 0 {
			out = append(out, Entry{Tool: rz.Need.Capability, Via: "apk", Verified: VerifiedDelegated, Purpose: purpose})
			continue
		}
		for _, p := range rz.Packages {
			out = append(out, Entry{Tool: p.Name, Version: p.Version, Via: "apk", Verified: VerifiedDelegated, Purpose: purpose})
		}
	}
	return out
}

// StageBox is THE way a phase presents the tools it prepared: it drains the phase's
// provisioning delta from the run ledger and renders a "Staged Tools" box to w. The
// convention — followed identically by every phase — is to call it ONCE, immediately
// before opening the phase's work section (output.NewSection), so the box lands in
// front of the work it enabled. No-op when the phase pulled nothing (or no ledger).
//
// This is the single, discoverable entry point; phases never touch the ledger's flush
// or call Render directly. Adding a new tool-using phase means one line — StageBox(ctx,
// w, color) — ahead of its work box, nothing else.
func StageBox(ctx context.Context, w io.Writer, color bool) {
	Render(w, flushCollected(ctx), color)
}

// Render writes the "Staged Tools" box: a fixed-column ledger of the given entries.
// A no-op when empty. This is the LOW-LEVEL box; phases go through StageBox (which
// drains the ctx delta and calls this). Direct callers are the exception — a subsystem
// rendering its own one-off row (see dependency) — guarded by the render-boundary
// ratchet so the phase path stays the norm.
func Render(w io.Writer, entries []Entry, color bool) {
	if len(entries) == 0 {
		return
	}
	sec := output.NewSection(w, "Staged Tools", 0, color)
	const row = "  %-16s %-9s %-9s %-10s %s"
	sec.Row(row, "tool", "version", "via", "verified", "purpose")
	for _, e := range entries {
		sec.Row(row, e.Tool, e.Version, e.Via, e.Verified, e.Purpose)
	}
	sec.Close()
}

// via collapses an artifact URL to its resolution path — the ecosystem/host we pulled
// from, not full provenance (origin repo, transport, cache belong in verbose/JSON).
func via(sourceURL string) string {
	switch {
	case sourceURL == "":
		return ""
	case strings.Contains(sourceURL, "github.com"):
		return "github"
	case strings.Contains(sourceURL, "go.dev"), strings.Contains(sourceURL, "golang.org"):
		return "go.dev"
	case strings.Contains(sourceURL, "rust-lang.org"):
		return "rust-lang"
	case strings.Contains(sourceURL, "dl.k8s.io"):
		return "k8s"
	default:
		return "https"
	}
}

// humanizeReason turns a substrate Need.Reason token into a plain-language purpose.
func humanizeReason(reason string) string {
	switch reason {
	case "go-tests-exec-git":
		return "for git-based tests (fixtures, system-git transport)"
	case "go-test-race-cgo":
		return "C compiler for the race detector (cgo)"
	case "rust-build-script-linking":
		return "linker for the Rust build"
	case "crate-native-build", "":
		return "native build dependency"
	default:
		return reason
	}
}
