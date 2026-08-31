package governance

import (
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
