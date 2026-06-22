// Package disk reports StageFreight's on-host disk footprint: the persistent
// cache mount (toolchain SDKs + Go/Rust build caches) and a workspace's
// .stagefreight/ artifacts (content store, scan reports, release artifacts).
// It is read-only and never creates directories — a `du` scoped to the paths
// StageFreight actually owns, so an operator can see what is hogging space.
package disk

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// Entry is one sized location, with an optional sub-breakdown (biggest first).
type Entry struct {
	Label    string  `json:"label"`
	Path     string  `json:"path"`
	Bytes    int64   `json:"bytes"`
	Children []Entry `json:"children,omitempty"`
}

// FS is the filesystem that holds the scanned locations.
type FS struct {
	Path  string `json:"path"`
	Total int64  `json:"total_bytes"`
	Free  int64  `json:"free_bytes"`
}

// Report is the full StageFreight disk footprint on this host.
type Report struct {
	FS     FS      `json:"filesystem"`
	Groups []Entry `json:"groups"`
	Total  int64   `json:"total_bytes"`
}

// friendlyLabels describes known directory names; unknown names render as-is.
// The cache layout is owned by toolchain/cache.go (see its cacheReadme).
var friendlyLabels = map[string]string{
	"toolchains": "toolchain SDKs",
	"cache":      "build caches",
	"go":         "go cache (modules + build)",
	"rust":       "rust cache (cargo + target)",
	"substrate":  "apk build packages",
	"objects":    "content store (built images)",
	"dist":       "release artifacts",
	"security":   "scan reports",
	"deps":       "dependency-update report",
	"provenance": "provenance attestations",
	"published":  "publication results",
	"manifests":  "manifests",
	"badges":     "badges",
	"reports":    "reports",
}

func label(name string) string {
	if l, ok := friendlyLabels[name]; ok {
		return l
	}
	return name
}

// Scan computes the footprint: the persistent cache mount (parent of the
// toolchains root, e.g. /stagefreight) and workspaceRoot/.stagefreight/. Absent
// locations are skipped, so it runs meaningfully on any host.
func Scan(workspaceRoot string) Report {
	var r Report
	mount := filepath.Dir(toolchain.PersistentCacheRoot()) // e.g. /stagefreight
	if g, ok := scanTree("persistent cache", mount, 2); ok {
		r.Groups = append(r.Groups, g)
	}
	if g, ok := scanTree("workspace", filepath.Join(workspaceRoot, ".stagefreight"), 1); ok {
		r.Groups = append(r.Groups, g)
	}
	for _, g := range r.Groups {
		r.Total += g.Bytes
	}
	target := mount
	if _, err := os.Stat(target); err != nil {
		target = workspaceRoot
	}
	r.FS = statFS(target)
	return r
}

// scanTree sizes path (recursive total) and, while depth > 0, breaks it down by
// immediate subdirectory, biggest first. ok=false when path is not a directory.
func scanTree(lbl, path string, depth int) (Entry, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Entry{}, false
	}
	e := Entry{Label: lbl, Path: path, Bytes: dirSize(path)}
	if depth > 0 {
		ents, _ := os.ReadDir(path)
		for _, c := range ents {
			if !c.IsDir() {
				continue
			}
			if child, ok := scanTree(label(c.Name()), filepath.Join(path, c.Name()), depth-1); ok {
				e.Children = append(e.Children, child)
			}
		}
		sort.SliceStable(e.Children, func(i, j int) bool {
			return e.Children[i].Bytes > e.Children[j].Bytes
		})
	}
	return e, true
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
