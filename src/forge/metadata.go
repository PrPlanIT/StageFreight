package forge

import (
	"context"
	"strings"
)

// RepoMetadata is the project-identity a `kind: metadata` target pushes to a forge repo.
// Empty/nil fields are left unchanged. Values are pre-resolved and (for Topics) already
// normalized by the caller; the forge impl just maps them to its API's capability.
type RepoMetadata struct {
	Description string   // short project description ("" = leave unchanged)
	Website     string   // external site URL / homepage ("" = leave unchanged)
	Topics      []string // discovery tags (nil = leave unchanged)
	LogoPath    string   // path to a project-avatar image ("" = leave unchanged)
}

// MetadataOutcome reports, per repo, what the provider actually did — so the caller can
// render an honest summary (set / skipped-because-unsupported / warned).
type MetadataOutcome struct {
	Set      []string // fields applied: "description" | "website" | "topics" | "logo"
	Skipped  []string // "field: reason" — the provider has no such field
	Warnings []string // non-fatal issues (e.g., a topic dropped, a body over cap)
}

func (o *MetadataOutcome) set(field string) { o.Set = append(o.Set, field) }
func (o *MetadataOutcome) skip(field, reason string) {
	o.Skipped = append(o.Skipped, field+": "+reason)
}
func (o *MetadataOutcome) warn(msg string) { o.Warnings = append(o.Warnings, msg) }

// MetadataSetter is the OPTIONAL capability interface a forge implements when it can set
// repo identity fields. Mirrors registry.Warner: the metadata contributor type-asserts it
// and skips-with-a-warning where a forge (or provider like Azure DevOps) doesn't implement
// it — so this stays off the core Forge interface and forces nothing on non-participants.
type MetadataSetter interface {
	UpdateRepoMetadata(ctx context.Context, meta RepoMetadata) (MetadataOutcome, error)
}

// NormalizeTopic lowers an authored topic to the strict lowercase-hyphenated form the
// forges require (GitHub/Gitea enforce it; GitLab is lenient but accepts it — the portable
// subset). Returns the normalized value and whether it changed, so the caller can warn.
// A topic that normalizes to empty (all-invalid) yields "".
func NormalizeTopic(s string) (normalized string, changed bool) {
	orig := s
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	// collapse runs of hyphens and trim edges
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out, out != orig
}
