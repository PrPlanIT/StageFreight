package postbuild

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// Registry metadata for badges is addressed by config id only: {registry.<id>.…}. There is
// no provider-literal shorthand (no {docker.*}/{harbor.*}) — a literal cannot name WHICH of
// several same-provider registries it means and would pick one by luck. The id resolves to a
// registry entry whose provider selects the fetcher: docker → Docker Hub API (the only source
// with pull counts); everything else (ghcr, harbor, quay, generic) → the OCI manifest API,
// where size is summed from the manifest and "updated" read from the image config's .created.

const regPrefix = "{registry."

// tagFields are the recognized {registry.<id>.tag.<tag>.<field>} suffixes, longest first so
// "size:raw" is matched before "size". A tag name may itself contain dots (v1.18.4), so the
// field is anchored from the right and everything before it is the tag.
var tagFields = []string{"size:raw", "size", "updated", "digest"}

// registryRef records, per referenced registry id, which tags need per-tag metadata and
// whether any repo-level field (pulls/stars) was referenced — so each registry is fetched
// once, for exactly what its tokens ask for.
type registryRef struct {
	tags     map[string]bool
	repoInfo bool
}

// registryInfo is the fetched, provider-agnostic metadata for one registry id.
type registryInfo struct {
	pulls int64
	stars int
	tags  map[string]gitver.TagInfo
}

// ResolveRegistryTemplates resolves every {registry.<id>...} token across the given values,
// fetching each referenced registry's metadata exactly once (dispatched by the registry's
// provider), and returns the resolved values in the same order. Values with no such token
// pass through untouched; an unknown id or a tag with no metadata resolves to empty, which
// the badge layer renders as "n/a".
func ResolveRegistryTemplates(ctx context.Context, values []string, cfg *config.Config) []string {
	refs := extractRegistryRefs(values)
	if len(refs) == 0 {
		return values
	}
	infos := make(map[string]registryInfo, len(refs))
	for id, ref := range refs {
		resolved := config.ResolveRegistryByID(id, cfg.Registries, cfg.Vars)
		if resolved == nil {
			continue
		}
		infos[id] = fetchRegistryInfo(ctx, resolved, ref)
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = resolveRegistryTokens(v, infos)
	}
	return out
}

// parseRegistryToken splits the inside of a {registry.…} token (without braces) into its
// registry id, kind ("tag" or "repo"), tag name (for kind "tag"), and field. Registry ids
// carry no dots (they are config slugs), so the id is the first dotted segment; a "tag."
// remainder is suffix-anchored on tagFields to separate a dotted tag from its field.
func parseRegistryToken(inner string) (id, kind, tag, field string) {
	dot := strings.IndexByte(inner, '.')
	if dot <= 0 {
		return "", "", "", ""
	}
	id = inner[:dot]
	rest := inner[dot+1:]
	if after, ok := strings.CutPrefix(rest, "tag."); ok {
		for _, f := range tagFields {
			if strings.HasSuffix(after, "."+f) {
				return id, "tag", after[:len(after)-len(f)-1], f
			}
		}
		return "", "", "", "" // unrecognized tag field
	}
	return id, "repo", "", rest // repo-level field: pulls / pulls:raw / stars
}

// extractRegistryRefs scans all values for {registry.<id>...} tokens, collecting per id the
// tag names and whether repo-level info is needed, so fetching is minimal and one-pass.
func extractRegistryRefs(values []string) map[string]*registryRef {
	refs := map[string]*registryRef{}
	for _, v := range values {
		s := v
		for {
			i := strings.Index(s, regPrefix)
			if i == -1 {
				break
			}
			rest := s[i+len(regPrefix):]
			close := strings.IndexByte(rest, '}')
			if close == -1 {
				break
			}
			id, kind, tag, _ := parseRegistryToken(rest[:close])
			if id != "" {
				r := refs[id]
				if r == nil {
					r = &registryRef{tags: map[string]bool{}}
					refs[id] = r
				}
				if kind == "tag" && tag != "" {
					r.tags[tag] = true
				} else if kind == "repo" {
					r.repoInfo = true
				}
			}
			s = rest[close+1:]
		}
	}
	return refs
}

// resolveRegistryTokens replaces every {registry.<id>...} token in s with its fetched value.
func resolveRegistryTokens(s string, infos map[string]registryInfo) string {
	if !strings.Contains(s, regPrefix) {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, regPrefix)
		if i == -1 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		rest := s[i+len(regPrefix):]
		close := strings.IndexByte(rest, '}')
		if close == -1 {
			b.WriteString(regPrefix)
			b.WriteString(rest)
			break
		}
		b.WriteString(resolveOneToken(rest[:close], infos))
		s = rest[close+1:]
	}
	return b.String()
}

func resolveOneToken(inner string, infos map[string]registryInfo) string {
	id, kind, tag, field := parseRegistryToken(inner)
	info, ok := infos[id]
	if id == "" || !ok {
		return ""
	}
	if kind == "tag" {
		ti, ok := info.tags[tag]
		if !ok {
			return ""
		}
		out, _ := gitver.FormatTagField(ti, field)
		return out
	}
	switch field {
	case "pulls":
		return gitver.FormatCount(info.pulls)
	case "pulls:raw":
		return strconv.FormatInt(info.pulls, 10)
	case "stars":
		return strconv.Itoa(info.stars)
	}
	return ""
}

// fetchRegistryInfo fetches one registry's metadata, dispatching by provider. Docker Hub is
// the only provider with a pull/star API; every other provider is treated as generic OCI and
// its per-tag size/digest/updated come from the manifest + config blob.
func fetchRegistryInfo(ctx context.Context, r *config.ResolvedRegistry, ref *registryRef) registryInfo {
	info := registryInfo{tags: map[string]gitver.TagInfo{}}
	tags := make([]string, 0, len(ref.tags))
	for t := range ref.tags {
		tags = append(tags, t)
	}

	if r.Provider == "docker" {
		ns, repo := splitFirst(r.Path)
		if ns == "" || repo == "" {
			return info
		}
		dh, err := gitver.FetchDockerHubInfo(ns, repo)
		if err != nil || dh == nil {
			return info
		}
		info.pulls = dh.Pulls
		info.stars = dh.Stars
		if len(tags) > 0 {
			client := &http.Client{Timeout: 10 * time.Second}
			for t, ti := range gitver.FetchTagInfo(client, ns, repo, tags) {
				info.tags[t] = ti
			}
		}
		return info
	}

	// Generic OCI (ghcr, harbor, quay, …): per-tag manifest metadata. No pulls/stars API.
	if len(tags) > 0 {
		for t, m := range registry.FetchOCITagInfo(ctx, r.URL, r.Path, tags, ociCredResolver(r.Credentials), r.Credentials) {
			info.tags[t] = gitver.TagInfo{Size: m.Size, Digest: m.Digest, LastUpdated: m.Created}
		}
	}
	return info
}

// ociCredResolver adapts a registry's credential env-prefix to the (user, pass) resolver the
// OCI token flow expects. Returns nil for an unset prefix so public images fetch anonymously.
func ociCredResolver(prefix string) func(string) (string, string) {
	if prefix == "" {
		return nil
	}
	return func(string) (string, string) {
		c := credentials.ResolvePrefix(prefix)
		return c.User, c.Secret
	}
}

// splitFirst splits "namespace/repo" on the first slash.
func splitFirst(path string) (string, string) {
	if i := strings.IndexByte(path, '/'); i > 0 {
		return path[:i], path[i+1:]
	}
	return "", ""
}
