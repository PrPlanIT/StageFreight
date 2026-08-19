package supplychain

// Snapshot is the immutable result of one dependency-discovery pass across a
// repository. It is produced exactly once per audition (see
// discovery.Discover) and shared read-only by every consumer — lint
// rendering and dependency-update — so registry lookups and vulnerability
// correlation run a single time instead of once per consumer.
//
// Snapshot performs no filtering of its own: consumers narrow to their own
// scope (e.g. by Dependency.File) as needed.
type Snapshot struct {
	Dependencies []Dependency
}

// UnverifiedVulns returns the dependencies whose OSV vulnerability scan could not
// complete (VulnScanError set). A non-empty result is a security COVERAGE GAP: these
// dependencies are NOT known-clean, and consumers must surface them loudly rather than
// let an unreachable OSV silently pass the pipeline as green.
func (s *Snapshot) UnverifiedVulns() []Dependency {
	var out []Dependency
	for _, d := range s.Dependencies {
		if d.VulnScanError != "" {
			out = append(out, d)
		}
	}
	return out
}
