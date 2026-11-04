package prune

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// The runner declares its Docker daemon's image GC: the daemon belongs to the runner,
// not to any repo it builds. Unset, the daemon keeps its images (Docker's own default).
const (
	DaemonGCMaxSizeEnv = "SF_DAEMON_GC_MAX_SIZE" // image store budget, e.g. "8GB"
	DaemonGCMaxAgeEnv  = "SF_DAEMON_GC_MAX_AGE"  // evict images not pulled within, e.g. "14d"
	DaemonGCMinAgeEnv  = "SF_DAEMON_GC_MIN_AGE"  // never evict images pulled within (default 2m)
)

const defaultDaemonMinAge = 2 * time.Minute

// daemonImageAction is the runner-declared image GC, or nil when the runner declares
// none or declares it unparseably (reported as the action's skip reason).
func daemonImageAction() []Action {
	size, age := os.Getenv(DaemonGCMaxSizeEnv), os.Getenv(DaemonGCMaxAgeEnv)
	if size == "" && age == "" {
		return nil
	}
	a := Action{Kind: KindDaemonImages, Class: ClassDeclared, Label: "daemon images",
		MinAge: defaultDaemonMinAge, Reason: "runner-declared image budget (" + DaemonGCMaxSizeEnv + ", " + DaemonGCMaxAgeEnv + ")"}
	var bad []string
	if size != "" {
		if n, err := config.ParseSize(size); err == nil && n > 0 {
			a.MaxSize = n
		} else {
			bad = append(bad, DaemonGCMaxSizeEnv+"="+size)
		}
	}
	if age != "" {
		if d, err := config.ParseDuration(age); err == nil && d > 0 {
			a.MaxAge = d
		} else {
			bad = append(bad, DaemonGCMaxAgeEnv+"="+age)
		}
	}
	if v := os.Getenv(DaemonGCMinAgeEnv); v != "" {
		if d, err := config.ParseDuration(v); err == nil && d >= 0 {
			a.MinAge = d
		} else {
			bad = append(bad, DaemonGCMinAgeEnv+"="+v)
		}
	}
	if len(bad) > 0 {
		a.Invalid = "unparseable " + strings.Join(bad, ", ")
	}
	return []Action{a}
}

// daemonImage is one image in the daemon's store and when it was last pulled or tagged.
type daemonImage struct {
	ID     string
	Tags   []string
	Pulled time.Time
}

// docker runs the docker CLI against the daemon the job reaches; a variable so tests
// can stand in for a daemon.
var docker = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", args...).Output()
}

// pruneDaemonImages evicts images no container uses, least recently pulled first: those
// pulled longer than MaxAge ago, then more until the image store fits MaxSize. An image
// pulled within MinAge is never evicted, so one a job just pulled survives until its
// container starts.
// note reports the image store's size before and after.
func pruneDaemonImages(ctx context.Context, a Action, confirm bool) (items []string, note, skipped string, err error) {
	if a.Invalid != "" {
		return nil, "", a.Invalid, nil
	}
	if _, ok := dockerDaemon(); !ok {
		return nil, "", "no docker daemon reachable", nil
	}
	imgs, err := daemonImages(ctx)
	if err != nil {
		return nil, "", "", err
	}
	inUse, err := imagesInUse(ctx)
	if err != nil {
		return nil, "", "", err
	}
	total, err := imageStoreBytes(ctx)
	if err != nil {
		return nil, "", "", err
	}
	before := total
	defer func() {
		if confirm && len(items) > 0 {
			if after, aerr := imageStoreBytes(ctx); aerr == nil {
				note = fmt.Sprintf("image store %s → %s", humanBytes(before), humanBytes(after))
			}
		}
	}()
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].Pulled.Before(imgs[j].Pulled) })
	now := time.Now()
	for _, im := range imgs {
		if inUse[im.ID] || im.Pulled.After(now.Add(-a.MinAge)) {
			continue
		}
		stale := a.MaxAge > 0 && im.Pulled.Before(now.Add(-a.MaxAge))
		if !stale && (a.MaxSize <= 0 || total <= a.MaxSize) {
			break // least recently pulled first: the rest are newer still
		}
		name := im.ID
		if len(im.Tags) > 0 {
			name = im.Tags[0]
		}
		if !confirm {
			items = append(items, fmt.Sprintf("%s (pulled %s)", name, im.Pulled.Format("2006-01-02")))
			continue
		}
		refs := im.Tags
		if len(refs) == 0 {
			refs = []string{im.ID}
		}
		if _, rerr := docker(ctx, append([]string{"image", "rm"}, refs...)...); rerr != nil {
			continue // referenced since listing, or a parent of another image: keep it
		}
		items = append(items, fmt.Sprintf("%s (pulled %s)", name, im.Pulled.Format("2006-01-02")))
		if a.MaxSize > 0 {
			if total, err = imageStoreBytes(ctx); err != nil {
				return items, "", "", err
			}
		}
	}
	return items, "", "", nil
}

// daemonImages lists every image with its last pull/tag time (its build time when it
// was never tagged locally).
func daemonImages(ctx context.Context) ([]daemonImage, error) {
	out, err := docker(ctx, "image", "ls", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}
	ids := uniqueFields(string(out))
	if len(ids) == 0 {
		return nil, nil
	}
	out, err = docker(ctx, append([]string{"image", "inspect", "--format", "{{json .}}"}, ids...)...)
	if err != nil {
		return nil, fmt.Errorf("inspecting images: %w", err)
	}
	var imgs []daemonImage
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var raw struct {
			ID       string   `json:"Id"`
			RepoTags []string `json:"RepoTags"`
			Created  string   `json:"Created"`
			Metadata struct {
				LastTagTime string `json:"LastTagTime"`
			} `json:"Metadata"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil || raw.ID == "" {
			continue
		}
		pulled, _ := time.Parse(time.RFC3339Nano, raw.Metadata.LastTagTime)
		if pulled.IsZero() || pulled.Year() < 2000 {
			pulled, _ = time.Parse(time.RFC3339Nano, raw.Created)
		}
		imgs = append(imgs, daemonImage{ID: raw.ID, Tags: raw.RepoTags, Pulled: pulled})
	}
	return imgs, nil
}

// imagesInUse is the set of image IDs any container, running or stopped, references.
func imagesInUse(ctx context.Context) (map[string]bool, error) {
	out, err := docker(ctx, "container", "ls", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	ids := uniqueFields(string(out))
	used := map[string]bool{}
	if len(ids) == 0 {
		return used, nil
	}
	out, err = docker(ctx, append([]string{"container", "inspect", "--format", "{{.Image}}"}, ids...)...)
	if err != nil {
		return nil, fmt.Errorf("inspecting containers: %w", err)
	}
	for _, id := range uniqueFields(string(out)) {
		used[id] = true
	}
	return used, nil
}

// imageStoreBytes is the image store's size as the daemon reports it, shared layers
// counted once.
func imageStoreBytes(ctx context.Context) (int64, error) {
	out, err := docker(ctx, "system", "df", "--format", "{{json .}}")
	if err != nil {
		return 0, fmt.Errorf("reading image store size: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var row struct{ Type, Size string }
		if json.Unmarshal([]byte(line), &row) == nil && row.Type == "Images" {
			return parseDockerSize(row.Size)
		}
	}
	return 0, fmt.Errorf("reading image store size: no Images row")
}

// parseDockerSize reads the daemon's human sizes ("11.25GB", "7.756MB", "0B"), which are
// decimal.
func parseDockerSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	mult := map[string]float64{"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15}
	for _, unit := range []string{"kB", "KB", "MB", "GB", "TB", "PB", "B"} {
		if num, ok := strings.CutSuffix(s, unit); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil {
				return 0, fmt.Errorf("size %q: %w", s, err)
			}
			return int64(f * mult[unit]), nil
		}
	}
	return 0, fmt.Errorf("size %q: unknown unit", s)
}

func uniqueFields(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range strings.Fields(s) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}
