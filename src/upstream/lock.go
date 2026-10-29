package upstream

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/PrPlanIT/StageFreight/src/paths"
)

// LockfileVersion is bumped only on a breaking change to the lock's shape.
const LockfileVersion = 1

// Lock is .stagefreight/upstream.lock: when StageFreight first saw each upstream version
// newer than our pins. It is the cooldown clock for upstreams that publish no release
// timestamp (git tags over ls-remote), written by the dependency update only when
// dependency.min_release_age is set, and pruned as pins move past versions.
type Lock struct {
	LockfileVersion int          `json:"lockfileVersion"`
	Sources         []LockSource `json:"sources"`
}

// LockSource is one source's first-seen record.
type LockSource struct {
	ID        string            `json:"id"`
	FirstSeen map[string]string `json:"first_seen"` // version → RFC 3339 UTC
}

// LockPath is the lock's location under the repository root.
func LockPath(root string) string { return paths.Durable(root, "upstream.lock") }

// ReadLock reads the lock; a missing lock is an empty one.
func ReadLock(root string) (*Lock, error) {
	data, err := os.ReadFile(LockPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return &Lock{LockfileVersion: LockfileVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

func (l *Lock) source(id string) *LockSource {
	for i := range l.Sources {
		if l.Sources[i].ID == id {
			return &l.Sources[i]
		}
	}
	l.Sources = append(l.Sources, LockSource{ID: id, FirstSeen: map[string]string{}})
	return &l.Sources[len(l.Sources)-1]
}

// Observe records the versions newer than our pin as seen now (keeping earlier sightings) and
// drops versions no longer newer. It returns when each version was first seen, any versions
// whose stored timestamp was corrupt and had to be reset, and whether the lock changed.
func (l *Lock) Observe(id string, newer []string, now time.Time) (firstSeen map[string]time.Time, healed []string, changed bool) {
	src := l.source(id)
	keep := map[string]bool{}
	firstSeen = map[string]time.Time{}
	for _, v := range newer {
		keep[v] = true
		at, ok := src.FirstSeen[v]
		if !ok {
			at = now.UTC().Format(time.RFC3339)
			src.FirstSeen[v] = at
			changed = true
		}
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			// A corrupt timestamp was silently reset to now every run, so the cooldown never
			// elapsed and the version never bumped. Heal it — reset to now once, conservatively
			// restarting the cooldown — and report it so the caller surfaces the reset loudly.
			t = now
			src.FirstSeen[v] = now.UTC().Format(time.RFC3339)
			healed = append(healed, v)
			changed = true
		}
		firstSeen[v] = t
	}
	for v := range src.FirstSeen {
		if !keep[v] {
			delete(src.FirstSeen, v)
			changed = true
		}
	}
	sort.Strings(healed)
	return firstSeen, healed, changed
}

// Write writes the lock deterministically, dropping sources with nothing recorded.
func (l *Lock) Write(root string) error {
	kept := l.Sources[:0]
	for _, s := range l.Sources {
		if len(s.FirstSeen) > 0 {
			kept = append(kept, s)
		}
	}
	l.Sources = kept
	sort.Slice(l.Sources, func(i, j int) bool { return l.Sources[i].ID < l.Sources[j].ID })
	l.LockfileVersion = LockfileVersion
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Durable(root), 0o755); err != nil {
		return err
	}
	return os.WriteFile(LockPath(root), append(data, '\n'), 0o644)
}
