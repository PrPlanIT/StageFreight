package disk

import (
	"os"
	"path/filepath"
)

// repoParents are dir names worth recursing into in search of repos.
var repoParents = map[string]bool{
	"repositories": true, "projects": true, "dev": true, "repos": true,
	"code": true, "src": true, "workspace": true, "git": true,
}

// skipDirs are never descended (build bloat, vcs internals, pseudo-fs).
var skipDirs = map[string]bool{
	"node_modules": true, "target": true, "vendor": true, ".git": true,
	".venv": true, "__pycache__": true, "dist": true, "build": true,
	".cache": true, "proc": true, "sys": true, "dev": false, // "dev" allowed (repo parent)
}

// bloatDirs are the reclaimable weight inside a repo, surfaced in its note.
var bloatDirs = []string{"target", "node_modules", ".git", "vendor", "dist", ".venv"}

// ScanRepos discovers git repos under roots (≤maxDepth deep) and sizes each at the
// repo-dir level, biggest-first. Returns nil if none found.
func ScanRepos(roots []string, maxDepth int) *Node {
	dom := &Node{Label: "REPOSITORIES", Note: "git repos ≤" + itoa(maxDepth) + " deep",
		Attr: Attribution{Runtime: "repo-tree"}}
	seen := map[string]bool{}
	for _, root := range roots {
		discoverRepos(root, 0, maxDepth, dom, seen)
	}
	if len(dom.Kids) == 0 {
		return nil
	}
	for _, c := range dom.Kids {
		dom.Bytes += c.Bytes
	}
	dom.sortKids()
	return dom
}

func discoverRepos(dir string, depth, maxDepth int, dom *Node, seen map[string]bool) {
	if depth > maxDepth || !isDir(dir) {
		return
	}
	base := filepath.Base(dir)
	if skipDirs[base] || (len(base) > 1 && base[0] == '.' && !repoParents[base]) {
		return
	}
	// A repo: has a .git entry (dir for normal, file for worktrees/submodules).
	if exists(filepath.Join(dir, ".git")) {
		if !seen[dir] {
			seen[dir] = true
			dom.add(&Node{Label: base, Path: dir, Bytes: dirSize(dir), Note: bloatNote(dir),
				Attr: Attribution{Project: base, Runtime: "repo-tree"}})
		}
		return // do not descend into a repo
	}
	for _, sub := range subdirs(dir) {
		discoverRepos(filepath.Join(dir, sub), depth+1, maxDepth, dom, seen)
	}
}

// bloatNote names the heaviest reclaimable subdirs inside a repo.
func bloatNote(dir string) string {
	var parts []string
	for _, b := range bloatDirs {
		p := filepath.Join(dir, b)
		if isDir(p) {
			if sz := dirSize(p); sz > 50<<20 { // only call out >50 MiB
				parts = append(parts, b+"/ "+humanBytesShort(sz))
			}
		}
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return joinDot(parts)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func joinDot(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " · "
		}
		out += p
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
