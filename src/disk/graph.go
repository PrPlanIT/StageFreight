// Package disk is a storage-attribution diagnostic for StageFreight: it answers
// "what operational entity (project / registry / runtime) is consuming disk
// today" rather than "what folders exist". Scanners build a graph of sized Nodes
// carrying attribution edges; the renderer and the by-project / reclaim views are
// projections of that one graph. Read-only.
package disk

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Flag marks a node for the two operator-facing concerns: attention and reclaim.
type Flag uint8

const (
	FlagAttention   Flag = 1 << iota // ⚠ smelly / stale / proliferating
	FlagReclaimable                  // ♻ safe (or safe-after-inspect) to delete
)

func (f Flag) Has(x Flag) bool { return f&x != 0 }

// Attribution links a storage node to operational entities — the projection axes.
// Empty fields mean "not attributable on this dimension".
type Attribution struct {
	Project  string // dragonfly, stagefreight, jetpack
	Registry string // ghcr.io, cr.pcfae.com, docker.io
	Runtime  string // cache-mount, docker-host, docker-dind, repo-tree
	Tool     string // go, rust, trivy
}

// Hint is the reclaim action surfaced in the ledger.
type Hint struct {
	Command string // "docker buildx prune"
	Safety  string // "safe", "inspect first", "rebuilds"
}

// Node is one sized location in the graph. Everything is the same shape:
//
//	label · TOTAL · note   →   children (biggest-first)
type Node struct {
	Label string
	Path  string // absolute, when backed by the filesystem
	Bytes int64  // self + descendants
	Note  string // inline diagnosis: versions, tags, "×6 · avg 1.0G"
	Attr  Attribution
	Flags Flag
	Hint  *Hint
	Kids  []*Node
}

func (n *Node) add(c *Node) {
	if c != nil {
		n.Kids = append(n.Kids, c)
	}
}

// sortKids orders children biggest-first (stable, so equal sizes keep insertion
// order — used where the scanner pre-sorts by recency).
func (n *Node) sortKids() {
	sort.SliceStable(n.Kids, func(i, j int) bool { return n.Kids[i].Bytes > n.Kids[j].Bytes })
	for _, c := range n.Kids {
		c.sortKids()
	}
}

// FS is the filesystem context the whole report is scaled against.
type FS struct {
	Path  string
	Total int64
	Free  int64
}

// Report is the assembled graph: top-level domains under an implicit root.
type Report struct {
	FS      FS
	Domains []*Node
}

// Total sums the domains (note: domains can overlap the same filesystem, so this
// is an attribution total, not a disk-occupancy total).
func (r *Report) Total() int64 {
	var t int64
	for _, d := range r.Domains {
		t += d.Bytes
	}
	return t
}

// Reclaimable is the reclaim-ledger projection: every node flagged reclaimable,
// biggest-first, deduplicated to the highest reclaimable ancestor (we never list
// both a parent and its child).
func (r *Report) Reclaimable() []*Node {
	var out []*Node
	var walk func(n *Node, parentReclaimable bool)
	walk = func(n *Node, parentReclaimable bool) {
		here := n.Flags.Has(FlagReclaimable)
		if here && !parentReclaimable {
			out = append(out, n)
		}
		for _, c := range n.Kids {
			walk(c, parentReclaimable || here)
		}
	}
	for _, d := range r.Domains {
		walk(d, false)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

// ProjectRow is one line of the by-project projection.
type ProjectRow struct {
	Project string
	Bytes   int64
	Parts   []*Node // the contributing nodes, biggest-first
}

// ByProject groups attributed nodes by project. Attribution emerges from
// independent scanners agreeing on the project key — nobody computes the rollup.
func (r *Report) ByProject() []ProjectRow {
	idx := map[string]*ProjectRow{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if p := n.Attr.Project; p != "" {
			row := idx[p]
			if row == nil {
				row = &ProjectRow{Project: p}
				idx[p] = row
			}
			row.Bytes += n.Bytes
			row.Parts = append(row.Parts, n)
			return // attributed node owns its subtree's bytes; don't double-count children
		}
		for _, c := range n.Kids {
			walk(c)
		}
	}
	for _, d := range r.Domains {
		walk(d)
	}
	out := make([]ProjectRow, 0, len(idx))
	for _, row := range idx {
		sort.SliceStable(row.Parts, func(i, j int) bool { return row.Parts[i].Bytes > row.Parts[j].Bytes })
		out = append(out, *row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

// ── filesystem helpers ──────────────────────────────────────────────────────

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// subdirs returns immediate subdirectory names, unsorted.
func subdirs(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// dirSize sums regular-file sizes under root (best-effort; unreadable entries
// skipped, symlinks not followed).
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func statFS(path string) FS {
	fs := FS{Path: path}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err == nil {
		fs.Total = int64(st.Blocks) * int64(st.Bsize)
		fs.Free = int64(st.Bavail) * int64(st.Bsize)
	}
	return fs
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return "/"
}
