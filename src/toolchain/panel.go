package toolchain

import (
	"fmt"
	"io"
)

// RenderPanel writes the toolchain resolution panel to w.
// Called once during audition after all tools are resolved.
func RenderPanel(w io.Writer, results []Result, cacheRoot string, elapsed fmt.Stringer) {
	fmt.Fprintf(w, "    ── Toolchain ──────────────────────────── %s ──\n", elapsed)
	for _, r := range results {
		status := "cache hit ✓"
		if !r.CacheHit {
			status = "downloaded ✓"
		}
		fmt.Fprintf(w, "    │ %-12s %-14s %s\n", r.Tool, r.Version, status)
	}
	if cacheRoot != "" {
		label := "workspace"
		if cacheRoot == persistentRoot {
			label = "persistent"
		}
		fmt.Fprintf(w, "    │ cache        %s (%s)\n", cacheRoot, label)
	}
	fmt.Fprintf(w, "    └─────────────────────────────────────────────────────\n")
}
