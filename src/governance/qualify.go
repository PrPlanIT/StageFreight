package governance

import (
	"fmt"
	"path"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

// PresetQualifier is the reference a satellite resolves unqualified presets against.
// Ref empty means the source's default branch, which is the tracking default.
type PresetQualifier struct {
	Repo string // clonable source, e.g. "https://gitlab.example.com/Org/Policy"
	Ref  string // "" = default branch (tracked); a branch tracks; a tag/sha pins
}

// Qualify turns an unqualified preset path into a reference that names where it comes
// from, so a satellite resolves it against the source instead of reading whatever copy
// it happens to hold.
//
// THE INVARIANT: a reference that already names its own source is returned untouched.
// Governance supplies provenance for a reference that has none; it never redirects one
// that does. Without this, adding governance to a config would silently drag a preset
// declared as https://elsewhere/x.yml back to the governance repo.
func (g PresetQualifier) Qualify(raw string) string {
	if g.Repo == "" {
		return raw
	}
	if presetref.Parse(raw).Kind != presetref.Local {
		return raw // already sourced — not ours to redirect
	}
	out := g.Repo + "//" + strings.TrimPrefix(path.Clean(raw), "./")
	if g.Ref != "" {
		out += "@" + g.Ref
	}
	return out
}

// NewPresetQualifier builds the qualifier for a distribution. When the source's
// coordinates are incomplete, it returns a zero qualifier that rewrites nothing: a
// half-formed source ("/" from an empty forge and project) would produce references
// no satellite could resolve, and leaving paths local is the honest fallback.
func NewPresetQualifier(ps PresetSourceInfo) PresetQualifier {
	if ps.ForgeURL == "" || ps.ProjectID == "" {
		return PresetQualifier{}
	}
	return PresetQualifier{
		Repo: strings.TrimSuffix(ps.ForgeURL, "/") + "/" + strings.Trim(ps.ProjectID, "/"),
		Ref:  ps.Ref,
	}
}

// QualifyConfig rewrites every unqualified preset:/presets: reference in a config so
// the distributed satellite resolves them from the source. Mutates in place.
func (g PresetQualifier) QualifyConfig(config map[string]any) {
	if g.Repo == "" {
		return
	}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if p, ok := t["preset"].(string); ok && p != "" {
				t["preset"] = g.Qualify(p)
			}
			if list, ok := t["presets"].([]any); ok {
				for i, item := range list {
					if p, ok := item.(string); ok && p != "" {
						list[i] = g.Qualify(p)
					}
				}
			}
			for _, v := range t {
				walk(v)
			}
		case []any:
			for _, v := range t {
				walk(v)
			}
		}
	}
	walk(config)
}

// retentionKey is where a preset is retained for later lookup. A sourced reference is
// keyed by its full identity (source, ref, path) so two sources sharing a path cannot
// collide; a local reference keeps its plain path, since CacheKey's separators would
// mangle it into "-/preset/x.yml" for no gain.
func retentionKey(ref string) string {
	r := presetref.Parse(ref)
	if r.Kind == presetref.Local {
		return r.Path
	}
	return presetref.CacheKey(r)
}

// isLocalRef reports whether a preset reference is an in-repo path, i.e. something the
// control repo can read and seed.
func isLocalRef(ref string) bool {
	return presetref.Parse(ref).Kind == presetref.Local
}

// PresetSourceFetcher retrieves a preset from a source outside the control repo. Set by
// a network-capable entry point (the CLI), left nil in tests and offline runs.
var PresetSourceFetcher presetref.Fetcher

// PresetMaterializeCache is where the control repo retains what it fetched while
// materializing. Every source must be reachable the first time, but not every time:
// with this, a pin is fetched once and a tracked source falls back to the last copy
// that resolved rather than failing the reconcile. Empty retains nothing.
var PresetMaterializeCache string

// loadPresetContent obtains a preset for seeding into a satellite's cache.
//
// Governance materializes EVERY reference it distributes, not only the ones it owns. A
// satellite that receives a config naming https://policies.example.org/security.yml and
// no retained copy of it has no fallback the first time that host is unreachable — and
// providing that fallback is the reason governance seeds a cache at all. So a local path
// is read from the control repo, and anything else is fetched from its own source.
func loadPresetContent(ref string, loader PresetLoader) ([]byte, error) {
	r := presetref.Parse(ref)
	if r.Kind == presetref.Local {
		return loader.Load(ref)
	}
	if PresetSourceFetcher == nil {
		return nil, fmt.Errorf("no network fetcher wired to materialize %q", ref)
	}
	var cache presetref.Cache = presetref.NoCache{}
	if PresetMaterializeCache != "" {
		cache = presetref.NewFSCache(PresetMaterializeCache)
	}
	return presetref.Resolver{Fetcher: PresetSourceFetcher, Cache: cache}.Resolve(r)
}
