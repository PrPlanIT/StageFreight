package toolchain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/PrPlanIT/StageFreight/src/paths"
)

// LockfileVersion is the schema version of .stagefreight/toolchains.lock. Bump only on
// a breaking shape change; a reader may then migrate older versions.
const LockfileVersion = 1

// LockEntry is the machine-maintained resolution of one tool: the concrete version a
// constraint currently resolves to, plus its artifact digest. For an EXACT constraint
// this simply mirrors the version; for a WILDCARD (1.26.x) it is the selected in-line
// member — the "lock" that keeps a wildcard reproducible between deliberate moves.
type LockEntry struct {
	Name     string `json:"name"`
	Resolved string `json:"resolved"`
	SHA256   string `json:"sha256,omitempty"`
}

// Lock is .stagefreight/toolchains.lock: the durable, committed record of what each
// toolchain constraint resolved to (the Cargo.lock to .stagefreight.yml's Cargo.toml).
// The config declares INTENT (constraints); this records the machine-maintained
// RESOLUTION. Entries are kept sorted by Name so serialization is deterministic.
// (Distinct from the install flock in lock.go — that guards a download directory; this
// is persisted project state.)
type Lock struct {
	LockfileVersion int         `json:"lockfileVersion"`
	Toolchains      []LockEntry `json:"toolchains"`
}

// lockRelPath is the repo-relative path of the lock (a durable committed artifact).
func lockRelPath() string { return paths.Durable("", "toolchains.lock") }

// LockPath returns the absolute path of the lock under rootDir.
func LockPath(rootDir string) string { return paths.Durable(rootDir, "toolchains.lock") }

// ReadLock loads the lock from rootDir. A missing file is not an error — it returns an
// empty lock (nothing resolved yet), so first-lock is just an empty lock being filled.
func ReadLock(rootDir string) (*Lock, error) {
	data, err := os.ReadFile(LockPath(rootDir))
	if err != nil {
		if os.IsNotExist(err) {
			return &Lock{LockfileVersion: LockfileVersion}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", lockRelPath(), err)
	}
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", lockRelPath(), err)
	}
	if l.LockfileVersion == 0 {
		l.LockfileVersion = LockfileVersion
	}
	return &l, nil
}

// Get returns the entry for a tool and whether it exists.
func (l *Lock) Get(name string) (LockEntry, bool) {
	for _, e := range l.Toolchains {
		if e.Name == name {
			return e, true
		}
	}
	return LockEntry{}, false
}

// Resolved returns the locked version of a tool, or "" when the tool is not locked.
func (l *Lock) Resolved(name string) string {
	if e, ok := l.Get(name); ok {
		return e.Resolved
	}
	return ""
}

// Set upserts a tool's resolution and keeps the entries sorted. Returns true if the lock
// changed (a new entry, or a different resolved/sha256), so callers can avoid a no-op
// write. An empty resolved is rejected — the lock never records "nothing".
func (l *Lock) Set(name, resolved, sha256 string) bool {
	if resolved == "" {
		return false
	}
	for i := range l.Toolchains {
		if l.Toolchains[i].Name == name {
			if l.Toolchains[i].Resolved == resolved && l.Toolchains[i].SHA256 == sha256 {
				return false // unchanged
			}
			l.Toolchains[i].Resolved = resolved
			l.Toolchains[i].SHA256 = sha256
			return true
		}
	}
	l.Toolchains = append(l.Toolchains, LockEntry{Name: name, Resolved: resolved, SHA256: sha256})
	sort.Slice(l.Toolchains, func(i, j int) bool { return l.Toolchains[i].Name < l.Toolchains[j].Name })
	return true
}

// WriteLock serializes the lock deterministically (sorted entries, stable indent) to
// rootDir, creating the .stagefreight/ namespace if needed. It is a durable committed
// artifact — the workspace ignore allowlist carves it out.
func WriteLock(rootDir string, l *Lock) error {
	if l.LockfileVersion == 0 {
		l.LockfileVersion = LockfileVersion
	}
	sort.Slice(l.Toolchains, func(i, j int) bool { return l.Toolchains[i].Name < l.Toolchains[j].Name })
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", lockRelPath(), err)
	}
	data = append(data, '\n')
	path := LockPath(rootDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating namespace for %s: %w", lockRelPath(), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", lockRelPath(), err)
	}
	return nil
}
