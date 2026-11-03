package prune

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/retention"
	"github.com/PrPlanIT/StageFreight/src/runner"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// ActionResult is one executed (or previewed) action's outcome.
type ActionResult struct {
	Action  Action
	Items   []string // what was (or would be) removed, human-labeled
	Freed   int64    // bytes freed (0 in dry-run and for daemon-side prunes that don't report)
	Skipped string   // non-fatal reason the action did nothing ("no docker daemon", …)
	Note    string   // how the action was narrowed, when it ran partially
	Err     error
}

// busyColdAge is how long a cache entry must have gone unused to be evicted while another
// run holds the cache. Go refreshes a build-cache entry's mtime on use once it is an hour
// old, so nothing a running build reads is this cold.
const busyColdAge = 24 * time.Hour

// Execute runs (confirm=true) or previews (confirm=false) the planned actions.
// Every executor fails toward leaving things alone: an artifact it cannot positively
// enumerate under its policy is not touched.
func Execute(ctx context.Context, actions []Action, confirm bool) []ActionResult {
	out := make([]ActionResult, 0, len(actions))
	held := map[string]bool{} // cache roots taken exclusively for this pass
	busy := map[string]bool{} // cache roots another run is using
	var releases []func()
	releaseAll := func() {
		for _, rel := range releases {
			rel()
		}
		releases = nil
	}
	defer releaseAll()
	lastLocked := -1
	for i, a := range actions {
		if a.LockRoot != "" {
			lastLocked = i
		}
	}
	for i, a := range actions {
		r := ActionResult{Action: a}
		if confirm && a.LockRoot != "" && !held[a.LockRoot] && !busy[a.LockRoot] {
			if release, ok := paths.ExclusiveCacheRoot(a.LockRoot); ok {
				held[a.LockRoot] = true
				releases = append(releases, release)
			} else {
				busy[a.LockRoot] = true
			}
		}
		cold := confirm && a.LockRoot != "" && busy[a.LockRoot]
		if cold {
			r.Note = "cache in use by another StageFreight run — only entries unused for 24h"
		}
		switch a.Kind {
		case KindEvictDir:
			if cold {
				r.Items, r.Freed, r.Err = evictDir(a.Path, max(a.MaxAge, busyColdAge), 0, confirm)
			} else {
				r.Items, r.Freed, r.Err = evictDir(a.Path, a.MaxAge, a.MaxSize, confirm)
			}
		case KindGoBuildCache:
			minAge := time.Duration(0)
			if cold {
				minAge = busyColdAge
			}
			r.Items, r.Freed, r.Err = evictGoBuildCache(a.Path, a.MaxSize, minAge, confirm)
		case KindToolchains:
			if cold {
				r.Note = ""
				r.Skipped = "cache in use by another StageFreight run — a toolchain may be in use"
				break
			}
			cands, err := toolchain.PlanVersionRetention(a.Path, a.Policy, a.Pins)
			if err != nil {
				r.Skipped = "no toolchain cache"
				break
			}
			for _, c := range cands {
				r.Items = append(r.Items, c.Tool+"/"+c.Version)
				if confirm {
					if err := removeAllWritable(c.Dir); err != nil {
						r.Err = err
					}
				}
			}
		case KindImageStream:
			r.Items, r.Skipped, r.Err = pruneImageStream(ctx, a, confirm)
		case KindBuildkit:
			r.Items, r.Skipped, r.Err = pruneBuilder(ctx, a, confirm)
		case KindDeclaredImages:
			r.Items, r.Skipped, r.Err = pruneDeclaredImages(ctx, a, confirm)
		case KindHostResidue:
			r.Items, r.Skipped, r.Err = pruneHostResidue(ctx, a, confirm)
		}
		out = append(out, r)
		// The cache root is released as soon as its last action is done, never held through
		// the daemon-side prunes that follow.
		if i == lastLocked {
			releaseAll()
		}
	}
	return out
}

// ── evict-dir: the shared age→size algorithm over a dir's immediate entries ──

func evictDir(dir string, maxAge time.Duration, maxSize int64, confirm bool) (items []string, freed int64, err error) {
	ents, rerr := os.ReadDir(dir)
	if rerr != nil {
		return nil, 0, nil // absent subsystem — nothing to do
	}
	type entry struct {
		name  string
		path  string
		size  int64
		mtime time.Time
	}
	var all []entry
	var total int64
	for _, e := range ents {
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		p := filepath.Join(dir, e.Name())
		sz := info.Size()
		if e.IsDir() {
			sz = dirSize(p)
		}
		all = append(all, entry{e.Name(), p, sz, info.ModTime()})
		total += sz
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mtime.Before(all[j].mtime) }) // oldest first

	evict := func(e entry) {
		items = append(items, fmt.Sprintf("%s (%s)", e.name, humanBytes(e.size)))
		freed += e.size
		total -= e.size
		if confirm {
			if rmErr := removeAllWritable(e.path); rmErr != nil {
				err = rmErr
			}
		}
	}
	kept := all[:0]
	if maxAge > 0 {
		cutoff := time.Now().Add(-maxAge)
		for _, e := range all {
			if e.mtime.Before(cutoff) {
				evict(e)
				continue
			}
			kept = append(kept, e)
		}
		all = kept
	}
	if maxSize > 0 {
		for _, e := range all { // still oldest-first
			if total <= maxSize {
				break
			}
			evict(e)
		}
	}
	return items, freed, err
}

// removeAllWritable is os.RemoveAll that first makes directories writable: Go's module
// cache is read-only.
func removeAllWritable(path string) error {
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// evictGoBuildCache trims GOCACHE to maxSize by evicting entry files least recently used
// first (Go refreshes an entry's mtime on use). Go's top-level files are kept.
func evictGoBuildCache(dir string, maxSize int64, minAge time.Duration, confirm bool) (items []string, freed int64, err error) {
	prefixes, rerr := os.ReadDir(dir)
	if rerr != nil {
		return nil, 0, nil // absent subsystem — nothing to do
	}
	type entry struct {
		path  string
		size  int64
		mtime time.Time
	}
	var all []entry
	var total int64
	for _, p := range prefixes {
		if !p.IsDir() || len(p.Name()) != 2 {
			continue
		}
		files, ferr := os.ReadDir(filepath.Join(dir, p.Name()))
		if ferr != nil {
			continue
		}
		for _, f := range files {
			info, ierr := f.Info()
			if ierr != nil || !info.Mode().IsRegular() {
				continue
			}
			all = append(all, entry{filepath.Join(dir, p.Name(), f.Name()), info.Size(), info.ModTime()})
			total += info.Size()
		}
	}
	if maxSize <= 0 || total <= maxSize {
		return nil, 0, nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mtime.Before(all[j].mtime) }) // least recently used first
	var count int
	cutoff := time.Now().Add(-minAge)
	for _, e := range all {
		if total <= maxSize || (minAge > 0 && e.mtime.After(cutoff)) {
			break // least recently used first: once one is too recent, the rest are too
		}
		if confirm {
			if rmErr := os.Remove(e.path); rmErr != nil && !os.IsNotExist(rmErr) {
				err = rmErr
				continue
			}
		}
		freed += e.size
		total -= e.size
		count++
	}
	if count > 0 {
		items = []string{fmt.Sprintf("%d least-recently-used entries (%s)", count, humanBytes(freed))}
	}
	return items, freed, err
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ── docker-side executors (CLI transport; absent daemon = clean skip) ──────────

type dockerImage struct {
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	CreatedAt  string `json:"CreatedAt"`
}

func listImages(ctx context.Context) ([]dockerImage, error) {
	out, err := exec.CommandContext(ctx, "docker", "image", "ls", "--format", "json").Output()
	if err != nil {
		return nil, err
	}
	var imgs []dockerImage
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var im dockerImage
		if json.Unmarshal([]byte(line), &im) == nil {
			imgs = append(imgs, im)
		}
	}
	return imgs, nil
}

func parseDockerTime(s string) time.Time {
	// docker ls CreatedAt: "2026-08-27 06:59:56 +0000 UTC"
	t, err := time.Parse("2006-01-02 15:04:05 -0700 MST", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// imageStore adapts one repo's tag set to the retention engine.
type imageStore struct {
	repo    string
	tags    []retention.Item
	confirm bool
	removed *[]string
}

func (s *imageStore) List(context.Context) ([]retention.Item, error) { return s.tags, nil }
func (s *imageStore) Delete(ctx context.Context, name string) error {
	*s.removed = append(*s.removed, s.repo+":"+name)
	if !s.confirm {
		return nil
	}
	return exec.CommandContext(ctx, "docker", "rmi", s.repo+":"+name).Run()
}

// pruneImageStream applies the repo's OWN publish-target retention to the local
// daemon's copies of that stream. Candidacy = the target's tag templates; anything
// not matching them — every other repo, every other tag — is out of scope entirely.
func pruneImageStream(ctx context.Context, a Action, confirm bool) (items []string, skipped string, err error) {
	imgs, lerr := listImages(ctx)
	if lerr != nil {
		return nil, "no docker daemon reachable", nil
	}
	byRepo := map[string][]retention.Item{}
	for _, im := range imgs {
		if im.Tag == "" || im.Tag == "<none>" {
			continue
		}
		repoSlug := im.Repository
		if i := strings.LastIndexByte(repoSlug, '/'); i >= 0 {
			repoSlug = repoSlug[i+1:]
		}
		for _, s := range a.Streams {
			if strings.EqualFold(repoSlug, s) {
				byRepo[im.Repository] = append(byRepo[im.Repository],
					retention.Item{Name: im.Tag, CreatedAt: parseDockerTime(im.CreatedAt)})
			}
		}
	}
	for repo, tags := range byRepo {
		store := &imageStore{repo: repo, tags: tags, confirm: confirm, removed: &items}
		if _, aerr := retention.Apply(ctx, store, a.Templates, a.Policy); aerr != nil {
			err = aerr
		}
	}
	return items, "", err
}

// pruneDeclaredImages evicts ONLY images matching the operator's declared refs —
// each under its own rules. First-match scoping per repo:tag; undeclared images are
// never candidates.
func pruneDeclaredImages(ctx context.Context, a Action, confirm bool) (items []string, skipped string, err error) {
	imgs, lerr := listImages(ctx)
	if lerr != nil {
		return nil, "no docker daemon reachable", nil
	}
	type stream struct {
		items   []retention.Item
		refIdx  int
		repoTag map[string]string // item name → full ref for delete
	}
	streams := map[string]*stream{}
	for _, im := range imgs {
		if im.Tag == "" || im.Tag == "<none>" {
			continue
		}
		full := im.Repository + ":" + im.Tag
		for ri, ref := range a.Refs {
			if ref.Match == "" {
				continue
			}
			pats := retention.TemplatesToPatterns([]string{ref.Match})
			if !config.MatchPatterns(pats, full) && !config.MatchPatterns(pats, im.Repository) {
				continue
			}
			key := fmt.Sprintf("%d/%s", ri, im.Repository)
			st := streams[key]
			if st == nil {
				st = &stream{refIdx: ri, repoTag: map[string]string{}}
				streams[key] = st
			}
			st.items = append(st.items, retention.Item{Name: full, CreatedAt: parseDockerTime(im.CreatedAt)})
			st.repoTag[full] = full
			break
		}
	}
	for _, st := range streams {
		// A declared ref IS the policy for its stream.
		ref := a.Refs[st.refIdx]
		policy := config.RetentionPolicy{
			KeepLast: ref.KeepLast, KeepDaily: ref.KeepDaily, KeepWeekly: ref.KeepWeekly,
			KeepMonthly: ref.KeepMonthly, KeepYearly: ref.KeepYearly, MaxAge: ref.MaxAge,
		}
		sort.Slice(st.items, func(i, j int) bool { return st.items[i].CreatedAt.After(st.items[j].CreatedAt) })
		keep := retention.ApplyPolicies(st.items, policy)
		for i, it := range st.items {
			if keep[i] {
				continue
			}
			items = append(items, it.Name)
			if confirm {
				if rmErr := exec.CommandContext(ctx, "docker", "rmi", st.repoTag[it.Name]).Run(); rmErr != nil {
					err = rmErr
				}
			}
		}
	}
	return items, "", err
}

// pruneBuilder reclaims the sf-builder's cache through its own API — never a volume rm.
//
// The buildx builder is created by the BUILD phase, but disk GC deliberately runs at
// the front of the pipeline, so at this point that builder does not exist yet and
// `buildx prune --builder` reports "no builder" on every host and in every mode. When
// a standalone buildkitd is configured, address the daemon directly instead: the
// builder is one way to reach the cache, not the cache itself.
func pruneBuilder(ctx context.Context, a Action, confirm bool) (items []string, skipped string, err error) {
	if addr := strings.TrimSpace(os.Getenv("BUILDKIT_HOST")); addr != "" {
		return pruneBuildkitd(ctx, addr, a, confirm)
	}
	args := []string{"buildx", "prune", "--builder", a.Builder, "--force"}
	if a.KeepStore != "" {
		// buildx renamed --keep-storage to --reserved-space; the old name still works
		// but warns, and will eventually go. Ask the installed buildx which it speaks
		// rather than guessing from a version number.
		args = append(args, reservedSpaceFlag(ctx), a.KeepStore)
	}
	if a.Until != "" {
		// buildx parses `until` with Go's time.ParseDuration, which has NO day unit —
		// StageFreight's own duration vocabulary does ("7d"), so it must be rendered
		// into hours here. Passing "7d" straight through is a hard error.
		if until := dockerDuration(a.Until); until != "" {
			args = append(args, "--filter", "until="+until)
		}
	}
	if !confirm {
		return []string{"docker " + strings.Join(args, " ")}, "", nil
	}
	if out, rerr := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); rerr != nil {
		reason := firstLine(string(out))
		if strings.Contains(reason, "no builder") {
			// Absence, not failure: this host simply doesn't run that builder.
			return nil, "no " + a.Builder + " on this host", nil
		}
		return nil, "buildx prune failed: " + reason, nil
	}
	return []string{"pruned via builder API"}, "", nil
}

// pruneBuildkitd reclaims a standalone buildkitd's cache over its own API, using the
// same endpoint and PKI the build path uses (BUILDKIT_HOST + BUILDKIT_CERT_PATH).
func pruneBuildkitd(ctx context.Context, addr string, a Action, confirm bool) (items []string, skipped string, err error) {
	certPath := os.Getenv("BUILDKIT_CERT_PATH")
	if certPath == "" {
		certPath = "/buildkit-certs"
	}
	ca := filepath.Join(certPath, "ca.pem")
	cert := filepath.Join(certPath, "cert.pem")
	key := filepath.Join(certPath, "key.pem")
	// TLS is mandatory when BUILDKIT_HOST is set — mirrors the build path, which
	// refuses to fall back to an insecure connection.
	for _, f := range []string{ca, cert, key} {
		if _, serr := os.Stat(f); serr != nil {
			return nil, "buildkitd TLS certs missing at " + certPath, nil
		}
	}
	args := []string{"--addr", addr, "--tlscacert", ca, "--tlscert", cert, "--tlskey", key, "prune"}
	if a.KeepStore != "" {
		flag := buildctlReservedFlag(ctx)
		val := a.KeepStore
		if flag == "--keep-storage" {
			// Legacy buildctl takes MEGABYTES as a bare number. A size string parses as
			// that many MB, so "8GB" silently becomes 8 petabytes and prunes nothing.
			n, perr := config.ParseSize(a.KeepStore)
			if perr != nil {
				return nil, "unparseable build_cache.local.retention.max_size: " + a.KeepStore, nil
			}
			val = strconv.FormatInt(n/(1<<20), 10)
		}
		args = append(args, flag, val)
	}
	if a.Until != "" {
		// buildctl takes a duration directly rather than buildx's `--filter until=`.
		if until := dockerDuration(a.Until); until != "" {
			args = append(args, "--keep-duration", until)
		}
	}
	if !confirm {
		return []string{"buildctl " + strings.Join(args, " ")}, "", nil
	}
	if out, rerr := exec.CommandContext(ctx, "buildctl", args...).CombinedOutput(); rerr != nil {
		return nil, "buildctl prune failed: " + firstLine(string(out)), nil
	}
	return []string{"pruned via buildkitd API"}, "", nil
}

// buildctlReservedFlag asks the installed buildctl which spelling it speaks, the same
// way reservedSpaceFlag does for buildx.
func buildctlReservedFlag(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "buildctl", "prune", "--help").CombinedOutput()
	if err == nil && strings.Contains(string(out), "--reserved-space") {
		return "--reserved-space"
	}
	return "--keep-storage"
}

// dockerDuration renders a StageFreight duration ("7d", "72h") into the Go-native
// form docker/buildx filters accept (no day unit). Returns "" for an unparseable
// value so a malformed policy degrades to "no age filter" rather than a failed prune.
func dockerDuration(s string) string {
	d, err := config.ParseDuration(s)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
}

// reservedSpaceFlag returns the storage-cap flag the installed buildx accepts:
// --reserved-space on current versions, --keep-storage on older ones.
func reservedSpaceFlag(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "docker", "buildx", "prune", "--help").CombinedOutput()
	if err == nil && strings.Contains(string(out), "--reserved-space") {
		return "--reserved-space"
	}
	return "--keep-storage"
}

// firstLine is the actionable head of a multi-line tool error — buildx prefixes
// deprecation warnings before the real ERROR line, so prefer the latter when present.
func firstLine(out string) string {
	var first string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "ERROR:") {
			return l
		}
		if first == "" {
			first = l
		}
	}
	return first
}

// pruneHostResidue runs the AUTHORIZED generic residue prunes (dangling layers,
// exited containers) with the declared age window.
// dockerDaemon reports whether a Docker daemon answers; a variable so tests can stand
// in for either runner kind.
var dockerDaemon = runner.DockerDaemon

func pruneHostResidue(ctx context.Context, a Action, confirm bool) (items []string, skipped string, err error) {
	// Host residue lives in a Docker daemon; without one there is none.
	if _, ok := dockerDaemon(); !ok {
		return nil, "no docker daemon reachable", nil
	}
	until := dockerDuration(a.Until) // docker has no day unit; SF's vocabulary does
	if until == "" {
		return nil, "unparseable age in cleanup policy: " + a.Until, nil
	}
	cmds := [][]string{
		{"image", "prune", "--force", "--filter", "until=" + until},
		{"container", "prune", "--force", "--filter", "until=" + until},
	}
	for _, c := range cmds {
		items = append(items, "docker "+strings.Join(c, " "))
		if confirm {
			if rerr := exec.CommandContext(ctx, "docker", c...).Run(); rerr != nil {
				err = rerr
			}
		}
	}
	return items, "", err
}

func humanBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%dB", b)
}
