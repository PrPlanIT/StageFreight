package lint

import (
	"path/filepath"
	"strings"
)

// MatchGlob matches a glob pattern supporting ** against a forward-slash path.
// Patterns and paths should use "/" separators.
// Exported wrapper so modules can reuse the engine's glob semantics.
func MatchGlob(pattern, path string) bool { return matchGlob(pattern, path) }

// matchGlob extends filepath.Match with support for "**" (zero or more path
// segments). Patterns without "**" delegate directly to filepath.Match.
func matchGlob(pattern, path string) bool {
	// Fast path: no ** — use stdlib.
	if !strings.Contains(pattern, "**") {
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	// Split at the first "**".
	idx := strings.Index(pattern, "**")
	prefix := pattern[:idx]                          // everything before **
	suffix := strings.TrimLeft(pattern[idx+2:], "/") // everything after **/

	// The prefix (before **) must match the start of path.
	if prefix != "" {
		prefix = strings.TrimRight(prefix, "/")
		if !strings.HasPrefix(path, prefix) {
			return false
		}
		// Advance past the matched prefix + separator.
		path = strings.TrimPrefix(path, prefix)
		path = strings.TrimLeft(path, "/")
	}

	// No suffix — ** at end matches everything remaining.
	if suffix == "" {
		return true
	}

	// Try matching suffix against every possible tail of path.
	// "tail" walks: "a/b/c", "b/c", "c".
	parts := strings.Split(path, "/")
	for i := 0; i <= len(parts); i++ {
		tail := strings.Join(parts[i:], "/")
		if matchGlob(suffix, tail) {
			return true
		}
	}

	return false
}
