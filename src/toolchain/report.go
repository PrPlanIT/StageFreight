package toolchain

import (
	"fmt"
	"io"
)

// Report writes a human-readable toolchain resolution summary to w.
// This is the ONLY human-output surface in the toolchain package.
// All other functions return structured data — no stderr, no prints.
func Report(w io.Writer, r Result) {
	status := "cache hit ✓"
	if !r.CacheHit {
		status = "downloaded ✓"
	}
	fmt.Fprintf(w, "    toolchain   %-10s %-14s %s\n", r.Tool, r.Version, status)
	fmt.Fprintf(w, "    source      %s\n", r.SourceURL)
	fmt.Fprintf(w, "    archive     %s\n", r.SHA256)
	fmt.Fprintf(w, "    binary      %s\n", r.BinSHA256)
	if r.Trust != "" {
		fmt.Fprintf(w, "    trust       %s\n", TrustLabel(r.Trust))
	}
}

// TrustLabel renders a trust source as a plain-language phrase for output — a
// trust-evaluation system communicates HOW CONFIDENTLY a tool was trusted, not just
// that it resolved.
func TrustLabel(trust string) string {
	switch trust {
	case TrustPinned:
		return "pinned — verified against the fingerprint in config"
	case TrustChecksum:
		return "checksum — verified against the upstream published digest"
	case TrustTOFU:
		return "TOFU — established on first use (no upstream claim); re-verified every run"
	default:
		return trust
	}
}
