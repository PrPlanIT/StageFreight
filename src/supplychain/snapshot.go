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
