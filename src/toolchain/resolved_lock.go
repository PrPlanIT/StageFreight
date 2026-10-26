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
const LockfileVersion = 2

// LockEntry is the machine-maintained resolution of one tool: the concrete version a
// constraint currently resolves to, plus its artifact digest per os/arch. For an EXACT constraint
// this simply mirrors the version; for a WILDCARD (1.26.x) it is the selected in-line
// member — the "lock" that keeps a wildcard reproducible between deliberate moves.
type LockEntry struct {
	Name     string            `json:"name"`
	Resolved string            `json:"resolved"`
	SHA256   map[string]string `json:"sha256,omitempty"` // os/arch → artifact digest
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
	var head struct {
		LockfileVersion int `json:"lockfileVersion"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", lockRelPath(), err)
	}
	if head.LockfileVersion != LockfileVersion {
		return nil, fmt.Errorf("%s is lockfileVersion %d, this StageFreight reads %d: delete it and the next dependency update rewrites it", lockRelPath(), head.LockfileVersion, LockfileVersion)
	}
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", lockRelPath(), err)
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

// Digest returns the recorded artifact digest of tool@version for platform, or "".
func (l *Lock) Digest(name, version, platform string) string {
	if e, ok := l.Get(name); ok && e.Resolved == version {
		return e.SHA256[platform]
	}
	return ""
}

// Set upserts a tool's resolution and its digest on platform, keeping entries sorted. A new
// resolved version discards the old version's digests. Returns true if the lock changed,
// so callers can avoid a no-op write. An empty resolved is rejected — the lock never
// records "nothing"; an empty sha256 records the version alone.
func (l *Lock) Set(name, resolved, platform, sha256 string) bool {
	if resolved == "" {
		return false
	}
	for i := range l.Toolchains {
		e := &l.Toolchains[i]
		if e.Name != name {
			continue
		}
		changed := false
		if e.Resolved != resolved {
			e.Resolved, e.SHA256, changed = resolved, nil, true
		}
		if sha256 != "" && e.SHA256[platform] != sha256 {
			if e.SHA256 == nil {
				e.SHA256 = map[string]string{}
			}
			e.SHA256[platform], changed = sha256, true
		}
		return changed
	}
	e := LockEntry{Name: name, Resolved: resolved}
	if sha256 != "" {
		e.SHA256 = map[string]string{platform: sha256}
	}
	l.Toolchains = append(l.Toolchains, e)
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
