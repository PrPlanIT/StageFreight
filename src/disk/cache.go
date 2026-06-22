package disk

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// versionCap is how many newest versions a toolchain lists inline before the tail
// collapses to "+ N older" — same cap pattern as docker image "+ N more".
const versionCap = 4

// DefaultCacheRoot is the persistent mount as seen inside a CI job container
// (/stagefreight). On a runner host it is mounted elsewhere (e.g.
// /opt/docker/gitlab-runner/stagefreight) — pass that to ScanCacheMount.
func DefaultCacheRoot() string {
	return filepath.Dir(toolchain.PersistentCacheRoot())
}

// DiscoverCacheRoot locates the persistent cache mount when --cache is not given:
// the container default (/stagefreight) or, on a runner host, a `…/stagefreight`
// directory carrying toolchains/ + cache/. Returns "" if none found. (Reading a
// runner's root-owned mount needs sudo.)
func DiscoverCacheRoot() string {
	candidates := []string{DefaultCacheRoot()}
	for _, g := range []string{
		"/opt/docker/*/stagefreight",
		"/opt/*/stagefreight",
		"/srv/*/stagefreight",
		"/srv/*/*/stagefreight",
		"/var/lib/*/stagefreight",
	} {
		m, _ := filepath.Glob(g)
		candidates = append(candidates, m...)
	}
	for _, c := range candidates {
		if isDir(filepath.Join(c, "cache")) || isDir(filepath.Join(c, "toolchains")) {
			return c
		}
	}
	return ""
}

// ScanCacheMount builds the CACHE MOUNT domain from <root>/cache (rebuildable
// build/scan caches) and <root>/toolchains (versioned tool installs). Returns nil
// if neither exists.
func ScanCacheMount(root string) *Node {
	cacheDir := filepath.Join(root, "cache")
	tcDir := filepath.Join(root, "toolchains")
	if !isDir(cacheDir) && !isDir(tcDir) {
		return nil
	}
	dom := &Node{Label: "CACHE MOUNT", Path: root, Attr: Attribution{Runtime: "cache-mount"}}
	if isDir(cacheDir) {
		dom.add(scanCacheCaches(cacheDir))
	}
	if isDir(tcDir) {
		dom.add(scanToolchains(tcDir))
	}
	for _, c := range dom.Kids {
		dom.Bytes += c.Bytes
	}
	dom.sortKids()
	return dom
}

// ── toolchains/ ─────────────────────────────────────────────────────────────

func scanToolchains(dir string) *Node {
	g := &Node{Label: "toolchains/", Path: dir, Bytes: dirSize(dir), Note: "versioned tool installs",
		Attr: Attribution{Runtime: "cache-mount"}}
	for _, tool := range subdirs(dir) {
		toolDir := filepath.Join(dir, tool)
		vers := subdirs(toolDir)
		sortVersionsDesc(vers)
		note, flags := toolVersionNote(vers)
		g.add(&Node{
			Label: tool, Path: toolDir, Bytes: dirSize(toolDir),
			Note: note, Flags: flags, Attr: Attribution{Runtime: "cache-mount", Tool: tool},
		})
	}
	g.sortKids()
	return g
}

// toolVersionNote renders versions inline (newest-first), capping the tail.
func toolVersionNote(vers []string) (string, Flag) {
	switch n := len(vers); {
	case n == 0:
		return "", 0
	case n == 1:
		return vers[0], 0
	default:
		var flags Flag
		if n > 2 {
			flags |= FlagAttention // multiple stale versions worth pruning
		}
		shown, tail := vers, ""
		if n > versionCap {
			shown = vers[:versionCap]
			tail = fmt.Sprintf(" + %d older", n-versionCap)
			flags |= FlagReclaimable
		}
		return fmt.Sprintf("%d versions · %s%s", n, strings.Join(shown, " · "), tail), flags
	}
}

// ── cache/ ──────────────────────────────────────────────────────────────────

// opaqueCaches hold content-hashed entries not worth listing — collapse to a count.
var opaqueCaches = map[string]string{"buildkit": "layer-sets", "lint": "result sets"}

func scanCacheCaches(dir string) *Node {
	g := &Node{Label: "cache/", Path: dir, Bytes: dirSize(dir), Note: "rebuildable build/scan caches",
		Flags: FlagReclaimable, Attr: Attribution{Runtime: "cache-mount"}}
	for _, name := range subdirs(dir) {
		g.add(scanCacheSubsystem(name, filepath.Join(dir, name)))
	}
	g.sortKids()
	return g
}

func scanCacheSubsystem(name, dir string) *Node {
	n := &Node{Label: name, Path: dir, Bytes: dirSize(dir), Flags: FlagReclaimable,
		Attr: Attribution{Runtime: "cache-mount"}}

	// Opaque hashed caches: collapse to "×N · avg".
	if unit, ok := opaqueCaches[name]; ok {
		kids := subdirs(dir)
		if c := len(kids); c > 0 {
			n.Note = fmt.Sprintf("%d %s · avg %s", c, unit, humanBytesShort(n.Bytes/int64(c)))
		}
		if h := reclaimHint(name); h != nil {
			n.Hint = h
		}
		return n
	}

	for _, child := range subdirs(dir) {
		cdir := filepath.Join(dir, child)
		cn := &Node{Label: cacheChildLabel(name, child), Path: cdir, Bytes: dirSize(cdir)}
		// rust/build and (defensively) any */build is project-keyed — attribute.
		if name == "rust" && child == "build" {
			cn.Note = "per-project"
			for _, proj := range subdirs(cdir) {
				pdir := filepath.Join(cdir, proj)
				cn.add(&Node{Label: proj, Path: pdir, Bytes: dirSize(pdir),
					Attr: Attribution{Project: proj, Runtime: "cache-mount"}})
			}
			cn.sortKids()
		}
		n.add(cn)
	}
	n.sortKids()
	if h := reclaimHint(name); h != nil {
		n.Hint = h
	}
	return n
}

// cacheChildLabel cleans the real on-disk child names into something legible while
// staying anchored to reality (grype's "6" / "grype-db-download…").
func cacheChildLabel(subsystem, child string) string {
	switch {
	case subsystem == "grype" && strings.HasPrefix(child, "grype-db-download"):
		return "download-staging"
	case subsystem == "grype" && isAllDigits(child):
		return "db-v" + child
	default:
		return child
	}
}

func reclaimHint(name string) *Hint {
	switch name {
	case "buildkit":
		return &Hint{Command: "docker buildx prune", Safety: "safe"}
	default:
		return nil // generic cache reclaim is covered by the "cold rebuild" note
	}
}

// ── version ordering ────────────────────────────────────────────────────────

// sortVersionsDesc orders version dir names newest-first ("1.26.4" before "1.24",
// "1.96.0-musl" after "1.96.0").
func sortVersionsDesc(v []string) {
	sort.SliceStable(v, func(i, j int) bool { return versionLess(v[j], v[i]) })
}

func versionLess(a, b string) bool {
	ka, kb := versionKey(a), versionKey(b)
	for i := 0; i < len(ka) && i < len(kb); i++ {
		if ka[i] != kb[i] {
			return ka[i] < kb[i]
		}
	}
	if len(ka) != len(kb) {
		return len(ka) < len(kb)
	}
	return a < b // tiebreak: plain "1.96.0" sorts before suffixed "1.96.0-musl"
}

func versionKey(v string) []int {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r < '0' || r > '9' })
	nums := make([]int, 0, len(fields))
	for _, f := range fields {
		if x, err := strconv.Atoi(f); err == nil {
			nums = append(nums, x)
		}
	}
	return nums
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
