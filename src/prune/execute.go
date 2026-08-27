package prune

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/retention"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// ActionResult is one executed (or previewed) action's outcome.
type ActionResult struct {
	Action  Action
	Items   []string // what was (or would be) removed, human-labeled
	Freed   int64    // bytes freed (0 in dry-run and for daemon-side prunes that don't report)
	Skipped string   // non-fatal reason the action did nothing ("no docker daemon", …)
	Err     error
}

// Execute runs (confirm=true) or previews (confirm=false) the planned actions.
// Every executor fails toward leaving things alone: an artifact it cannot positively
// enumerate under its policy is not touched.
func Execute(ctx context.Context, actions []Action, confirm bool) []ActionResult {
	out := make([]ActionResult, 0, len(actions))
	for _, a := range actions {
		r := ActionResult{Action: a}
		switch a.Kind {
		case KindEvictDir:
			r.Items, r.Freed, r.Err = evictDir(a.Path, a.MaxAge, a.MaxSize, confirm)
		case KindToolchains:
			cands, err := toolchain.PlanVersionRetention(a.Path, a.Policy, a.Pins)
			if err != nil {
				r.Skipped = "no toolchain cache"
				break
			}
			for _, c := range cands {
				r.Items = append(r.Items, c.Tool+"/"+c.Version)
				if confirm {
					if err := os.RemoveAll(c.Dir); err != nil {
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
			if rmErr := os.RemoveAll(e.path); rmErr != nil {
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
func pruneBuilder(ctx context.Context, a Action, confirm bool) (items []string, skipped string, err error) {
	args := []string{"buildx", "prune", "--builder", a.Builder, "--force"}
	if a.KeepStore != "" {
		args = append(args, "--keep-storage", a.KeepStore)
	}
	if a.Until != "" {
		args = append(args, "--filter", "until="+a.Until)
	}
	if !confirm {
		return []string{"docker " + strings.Join(args, " ")}, "", nil
	}
	if out, rerr := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); rerr != nil {
		return nil, "builder unavailable: " + strings.TrimSpace(string(out)), nil
	}
	return []string{"pruned via builder API"}, "", nil
}

// pruneHostResidue runs the AUTHORIZED generic residue prunes (dangling layers,
// exited containers) with the declared age window.
func pruneHostResidue(ctx context.Context, a Action, confirm bool) (items []string, skipped string, err error) {
	cmds := [][]string{
		{"image", "prune", "--force", "--filter", "until=" + a.Until},
		{"container", "prune", "--force", "--filter", "until=" + a.Until},
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
