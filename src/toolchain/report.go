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
}
