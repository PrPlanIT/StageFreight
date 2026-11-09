package dependency

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"

	"github.com/PrPlanIT/StageFreight/src/paths"
)

// overridesLockVersion is bumped only on a breaking change to the lock's shape.
const overridesLockVersion = 1

// overridesLock is .stagefreight/overrides.lock: the override floors StageFreight wrote
// into package manifests. It is what lets StageFreight remove its own floor once the
// dependency graph outgrows it while never touching one an operator wrote. An entry
// whose manifest value no longer matches its floor has been taken over by a person and
// is dropped from the lock, not from the manifest.
type overridesLock struct {
	LockfileVersion int             `json:"lockfileVersion"`
	Overrides       []overrideFloor `json:"overrides"`
}

// overrideFloor is one StageFreight-written override.
type overrideFloor struct {
	Manifest   string   `json:"manifest"` // repo-relative package.json
	Package    string   `json:"package"`
	Floor      string   `json:"floor"`      // the value written, e.g. "^0.5.1"
	Advisories []string `json:"advisories"` // what it remediates
}

func overridesLockPath(root string) string { return paths.Durable(root, "overrides.lock") }

func readOverridesLock(root string) (*overridesLock, error) {
	data, err := os.ReadFile(overridesLockPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return &overridesLock{LockfileVersion: overridesLockVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var l overridesLock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// write persists the lock, deleting the file when it records nothing.
func (l *overridesLock) write(root string) error {
	path := overridesLockPath(root)
	if len(l.Overrides) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	sort.Slice(l.Overrides, func(i, j int) bool {
		a, b := l.Overrides[i], l.Overrides[j]
		if a.Manifest != b.Manifest {
			return a.Manifest < b.Manifest
		}
		return a.Package < b.Package
	})
	l.LockfileVersion = overridesLockVersion
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.Durable(root), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// forManifest returns the entries for one manifest.
func (l *overridesLock) forManifest(manifest string) []overrideFloor {
	var out []overrideFloor
	for _, o := range l.Overrides {
		if o.Manifest == manifest {
			out = append(out, o)
		}
	}
	return out
}

// set records (or replaces) the floor for manifest/package.
func (l *overridesLock) set(f overrideFloor) {
	l.drop(f.Manifest, f.Package)
	l.Overrides = append(l.Overrides, f)
}

// drop forgets manifest/package.
func (l *overridesLock) drop(manifest, pkg string) {
	kept := l.Overrides[:0]
	for _, o := range l.Overrides {
		if o.Manifest != manifest || o.Package != pkg {
			kept = append(kept, o)
		}
	}
	l.Overrides = kept
}
