// Package presetref parses and classifies a preset reference — the string in a
// `preset:` field that locates a preset fragment, optionally from a remote source at a
// git ref. Classification decides how the loader resolves it (local read, live fetch,
// or cache-authoritative), per the identity model's preset-resolution design. This is
// pure parsing and static classification — no I/O; the one genuinely source-dependent
// case (a bare ref name that could be a branch or a tag) is marked Named and left for
// the fetch layer to resolve against the source.
package presetref

import "strings"

// Kind is how a preset reference resolves.
type Kind int

const (
	// Local is an in-repo path (no source): read from the working tree / preset-cache.
	Local Kind = iota
	// Tracked is a source at a branch (or no ref): fetched live each run so a change at
	// the source propagates to every tracking repo, with the cache as offline fallback.
	Tracked
	// Pinned is a source at a provably-immutable ref (a sha or a refs/tags/ tag):
	// cache-authoritative — resolved from cache when present, fetched once otherwise.
	Pinned
	// Named is a source at a BARE ref name (e.g. "main", "v1.0") that can't be classified
	// statically — a branch and a tag look identical as strings. The fetch layer resolves
	// it against the source (a tag → pinned, a branch → tracked). Pinning explicitly
	// (a sha or refs/tags/…) avoids this deferral.
	Named
)

func (k Kind) String() string {
	switch k {
	case Local:
		return "local"
	case Tracked:
		return "tracked"
	case Pinned:
		return "pinned"
	case Named:
		return "named"
	default:
		return "unknown"
	}
}

// Ref is a parsed preset reference.
type Ref struct {
	Raw    string // the original string
	Kind   Kind
	Source string // "<source>" (forge repo or URL); "" for Local
	Path   string // preset path within the source, or the local path for Local
	Ref    string // the git ref (branch / tag / sha, possibly refs/-qualified); "" = default branch
}

// sourceSep separates the source from the in-source path: "<source>//<path>".
const sourceSep = "//"

// splitSource splits "<source>//<path>" on the source separator, skipping a URL
// scheme's own "//" (e.g. the one in "https://") so it isn't mistaken for the separator.
// Returns sourced=false when there is no separator (a plain local path).
func splitSource(raw string) (src, rest string, sourced bool) {
	from := 0
	if i := strings.Index(raw, "://"); i >= 0 {
		from = i + len("://")
	}
	j := strings.Index(raw[from:], sourceSep)
	if j < 0 {
		return "", "", false
	}
	idx := from + j
	return raw[:idx], raw[idx+len(sourceSep):], true
}

// isURL reports whether raw is an http(s) URL. Only these two schemes: another scheme
// (ssh://, git://) addresses a repository, which reaches Parse through the separator
// form and keeps its repo/path split.
func isURL(raw string) bool {
	return strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://")
}

// Parse classifies a preset reference. Forms:
//
//	preset/lint.yml                          → Local
//	https://example.org/lint.yml             → Tracked (the URL is the whole source)
//	gitlab:Org/Repo//preset/lint.yml         → Tracked (default branch)
//	…//preset/lint.yml@main                  → Named (bare — resolved at fetch)
//	…//preset/lint.yml@refs/heads/main       → Tracked
//	…//preset/lint.yml@refs/tags/v1.0        → Pinned
//	…//preset/lint.yml@v1.0                  → Named (bare — resolved at fetch)
//	…//preset/lint.yml@1a2b3c4               → Pinned (sha)
func Parse(raw string) Ref {
	r := Ref{Raw: raw}

	src, rest, sourced := splitSource(raw)
	if !sourced {
		// An http(s) URL with no separator: the URL IS the source. There is no repo/path
		// boundary to divide, so it stays one identity and nothing is split off as a path
		// — including a '@', which is legal in a URL and names no revision. A URL has no
		// revision semantics at all, so it is Tracked: the current response is what the
		// reference denotes, with the retained response as the fallback.
		if isURL(raw) {
			r.Kind = Tracked
			r.Source = raw
			return r
		}
		// No source separator → a local, in-repo path.
		r.Kind = Local
		r.Path = raw
		return r
	}
	r.Source = src

	// Split an optional @ref off the end of the path. A path holds no '@' and a ref holds
	// no '@', so the last '@' cleanly separates them.
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		r.Path = rest[:at]
		r.Ref = rest[at+1:]
	} else {
		r.Path = rest
	}

	r.Kind = classify(r.Ref)
	return r
}

// classify maps a git ref to a Kind, deterministically for every case except a bare
// name (Named), which only the source can resolve.
func classify(ref string) Kind {
	switch {
	case ref == "":
		return Tracked // default branch
	case isHexSHA(ref):
		return Pinned
	case strings.HasPrefix(ref, "refs/tags/"):
		return Pinned
	case strings.HasPrefix(ref, "refs/heads/"):
		return Tracked
	default:
		return Named // bare branch-or-tag name — resolved at fetch
	}
}

// isHexSHA reports whether ref is a git object id: 7–40 hex chars (abbreviated or full).
func isHexSHA(ref string) bool {
	if len(ref) < 7 || len(ref) > 40 {
		return false
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
