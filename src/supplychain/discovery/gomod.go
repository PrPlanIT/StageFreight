package discovery

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// goProxyLatest is the JSON response from proxy.golang.org/{mod}/@latest.
type goProxyLatest struct {
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
}

// checkGoMod parses go.mod and resolves latest versions via proxy.golang.org.
func (m *Resolver) checkGoMod(ctx context.Context, file lint.FileInfo) ([]supplychain.Dependency, error) {
	if !m.cfg.SourceEnabled(supplychain.EcosystemGoMod) {
		return nil, nil
	}

	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var deps []supplychain.Dependency
	replaced := map[string]bool{} // modules governed by a replace directive
	scanner := bufio.NewScanner(f)
	lineNum := 0
	inRequire := false
	inReplace := false
	projectGo := "" // the module's `go` directive — the Go version an autonomous bump must stay within

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// The top-level `go` directive is the Go version this module builds with. A dependency
		// whose own `go` directive is newer cannot be adopted here (`go get` refuses), so the
		// resolver holds it for review rather than proposing a build-breaking bump.
		if projectGo == "" && !inRequire && !inReplace && strings.HasPrefix(line, "go ") {
			if parts := strings.Fields(line); len(parts) >= 2 {
				projectGo = parts[1]
			}
			continue
		}

		// Handle require block
		if line == "require (" {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}

		// Handle replace directives (block + single-line). The replaced module is the
		// left-hand side of "=>"; discovery marks it Pinned so it is neither resolved
		// nor reported outdated — the go toolchain has already selected its version.
		if line == "replace (" {
			inReplace = true
			continue
		}
		if inReplace && line == ")" {
			inReplace = false
			continue
		}
		if strings.HasPrefix(line, "replace ") && strings.Contains(line, "=>") {
			if name := replacedModule(strings.TrimPrefix(line, "replace ")); name != "" {
				replaced[name] = true
			}
			continue
		}
		if inReplace && strings.Contains(line, "=>") {
			if name := replacedModule(line); name != "" {
				replaced[name] = true
			}
			continue
		}

		// Single-line require: require github.com/foo/bar v1.2.3
		if strings.HasPrefix(line, "require ") && !strings.HasSuffix(line, "(") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				dep := supplychain.Dependency{
					Name:      parts[1],
					Current:   parts[2],
					Ecosystem: supplychain.EcosystemGoMod,
					File:      file.Path,
					Line:      lineNum,
				}
				deps = append(deps, dep)
			}
			continue
		}

		// Inside require block
		if inRequire {
			// Skip comments
			if strings.HasPrefix(line, "//") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			indirect := strings.Contains(line, "// indirect")
			dep := supplychain.Dependency{
				Name:      parts[0],
				Current:   parts[1],
				Ecosystem: supplychain.EcosystemGoMod,
				File:      file.Path,
				Line:      lineNum,
				Indirect:  indirect,
			}
			deps = append(deps, dep)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Mark replace-governed modules before resolution — the toolchain has already
	// chosen their version, so they are pinned, not update candidates.
	for i := range deps {
		if replaced[deps[i].Name] {
			deps[i].Pinned = "replace directive"
		}
	}

	// Resolve latest versions
	for i := range deps {
		// Skip indirect (managed transitively) and replace-pinned (toolchain-governed).
		if deps[i].Indirect || deps[i].Pinned != "" {
			continue
		}
		m.resolveGoModule(ctx, &deps[i], projectGo)
	}

	return deps, nil
}

// replacedModule extracts the replaced module path (the left-hand side of "=>")
// from the body of a replace directive, e.g. "example.com/a v1 => ./local" → "example.com/a".
// Returns "" if there is no "=>" or no left-hand module.
func replacedModule(body string) string {
	i := strings.Index(body, "=>")
	if i < 0 {
		return ""
	}
	lhs := strings.Fields(strings.TrimSpace(body[:i]))
	if len(lhs) == 0 {
		return ""
	}
	return lhs[0]
}

// resolveGoModule queries proxy.golang.org (or custom registry) for the latest version.
// projectGo is the consuming module's `go` directive; a candidate declaring a newer Go is held.
func (m *Resolver) resolveGoModule(ctx context.Context, dep *supplychain.Dependency, projectGo string) {
	ep := m.cfg.registryEndpoint(supplychain.EcosystemGoMod)
	baseURL := m.cfg.registryURL(supplychain.EcosystemGoMod, "https://proxy.golang.org")
	// The module proxy protocol case-encodes the path: every uppercase letter becomes
	// "!"+lowercase. Without it, any module with an uppercase letter (e.g.
	// github.com/Masterminds/...) 404s and is silently reported "unresolved".
	url := fmt.Sprintf("%s/%s/@latest", strings.TrimRight(baseURL, "/"), escapeModPath(dep.Name))
	dep.SourceURL = url

	var resp goProxyLatest
	if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
		return
	}
	if resp.Version != "" {
		dep.Latest = resp.Version
		setReleased(dep, resp.Version, resp.Time)
	}

	// A candidate whose own `go` directive is newer than the project's cannot be adopted
	// autonomously: `go get` refuses (and the newer Go may not even be released), breaking the
	// build. Hold at the newest release the project CAN build — the newest Go-compatible version
	// above the current pin, else the current pin — keeping Latest as awareness so it surfaces
	// as held-for-review, never auto-applied.
	if projectGo != "" && dep.Latest != "" {
		if req := m.moduleGoRequirement(ctx, dep.Name, dep.Latest, baseURL, ep); req != "" && !goVersionSatisfies(projectGo, req) {
			dep.LatestEligible = m.newestGoCompatible(ctx, dep, projectGo, baseURL, ep)
			dep.Advisory = fmt.Sprintf("%s requires go >= %s; this module builds with go %s — held for review", dep.Latest, req, projectGo)
		}
	}

	// The full version list serves a patch-lock ceiling (the deps layer re-targets to the
	// newest patch of the current minor; @latest gives only the single newest) and a
	// cooldown holding a young latest (stepping back needs the older releases). Fetched
	// only then, so the default path makes no extra request. Best-effort: a list failure
	// leaves the dep as resolved above.
	young := resp.Version != "" && m.inCooldown(*dep, resp.Time)
	if !m.cfg.FetchVersionLists && !young {
		return
	}
	listURL := fmt.Sprintf("%s/%s/@v/list", strings.TrimRight(baseURL, "/"), escapeModPath(dep.Name))
	data, err := m.http.fetchBytes(ctx, listURL, ep)
	if err != nil {
		return
	}
	var newer []string
	for _, line := range strings.Split(string(data), "\n") {
		v := strings.TrimSpace(line)
		if v == "" {
			continue
		}
		if m.cfg.FetchVersionLists {
			dep.AvailableVersions = append(dep.AvailableVersions, v)
		}
		if !version.IsPrerelease(v) && compare(v, dep.Current, dep.Ecosystem) > 0 && v != resp.Version {
			newer = append(newer, v)
		}
	}
	if !young {
		return
	}
	// Date the releases between Current and latest, newest first, so the cooldown can
	// step back — stopping at the first one past every window, since anything older is
	// older still. Bounded, for a module with a burst of young releases.
	sort.Slice(newer, func(i, j int) bool { return compare(newer[i], newer[j], dep.Ecosystem) > 0 })
	if len(newer) > maxDatedGoVersions {
		newer = newer[:maxDatedGoVersions]
	}
	for _, v := range newer {
		var info goProxyLatest
		if err := m.http.fetchJSON(ctx, fmt.Sprintf("%s/%s/@v/%s.info", strings.TrimRight(baseURL, "/"), escapeModPath(dep.Name), v), &info, ep); err != nil {
			continue
		}
		setReleased(dep, v, info.Time)
		if !m.inCooldown(*dep, info.Time) {
			break
		}
	}
}

// maxDatedGoVersions bounds the per-version .info lookups a cooldown makes for one module.
const maxDatedGoVersions = 20

// moduleGoRequirement returns the `go` directive a module version declares in its go.mod,
// fetched from the proxy, or "" when none is declared or the fetch fails (then no gate applies).
func (m *Resolver) moduleGoRequirement(ctx context.Context, name, version, baseURL string, ep *RegistryEndpoint) string {
	url := fmt.Sprintf("%s/%s/@v/%s.mod", strings.TrimRight(baseURL, "/"), escapeModPath(name), version)
	data, err := m.http.fetchBytes(ctx, url, ep)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if parts := strings.Fields(line); len(parts) >= 2 && parts[0] == "go" {
			return parts[1]
		}
	}
	return ""
}

// newestGoCompatible returns the newest non-prerelease version above the current pin whose
// declared Go requirement the project satisfies, or the current pin when none qualifies. It is
// consulted only when the latest version itself is incompatible, so the module still advances
// as far as it safely can instead of being frozen at the incompatible tip.
func (m *Resolver) newestGoCompatible(ctx context.Context, dep *supplychain.Dependency, projectGo, baseURL string, ep *RegistryEndpoint) string {
	data, err := m.http.fetchBytes(ctx, fmt.Sprintf("%s/%s/@v/list", strings.TrimRight(baseURL, "/"), escapeModPath(dep.Name)), ep)
	if err != nil {
		return dep.Current
	}
	var newer []string
	for _, line := range strings.Split(string(data), "\n") {
		v := strings.TrimSpace(line)
		if v == "" || version.IsPrerelease(v) {
			continue
		}
		if compare(v, dep.Current, dep.Ecosystem) > 0 {
			newer = append(newer, v)
		}
	}
	sort.Slice(newer, func(i, j int) bool { return compare(newer[i], newer[j], dep.Ecosystem) > 0 })
	if len(newer) > maxDatedGoVersions {
		newer = newer[:maxDatedGoVersions]
	}
	for _, v := range newer {
		if req := m.moduleGoRequirement(ctx, dep.Name, v, baseURL, ep); req == "" || goVersionSatisfies(projectGo, req) {
			return v
		}
	}
	return dep.Current
}

// goVersionSatisfies reports whether a Go version `have` meets requirement `need` — both written
// as a dotted release ("1.27", "1.26.10"), optionally go-prefixed. Missing components are zero.
func goVersionSatisfies(have, need string) bool {
	ph, pn := goVersionParts(have), goVersionParts(need)
	for i := 0; i < 3; i++ {
		if ph[i] != pn[i] {
			return ph[i] > pn[i]
		}
	}
	return true
}

// goVersionParts splits a Go version into [major, minor, patch], reading the leading integer of
// each dotted component so a pre-release suffix (1.27rc1) degrades to its release ordinal.
func goVersionParts(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(v, "go")
	for i, s := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		n := 0
		for _, r := range s {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out[i] = n
	}
	return out
}

// escapeModPath applies the Go module proxy's case-encoding — every uppercase letter
// becomes "!" followed by its lowercase form (e.g. "Masterminds" → "!masterminds") —
// as required by the proxy protocol (golang.org/ref/mod#goproxy-protocol). Lowercase
// paths pass through unchanged.
func escapeModPath(p string) string {
	if p == strings.ToLower(p) {
		return p // no uppercase — nothing to encode
	}
	var b strings.Builder
	b.Grow(len(p) + 8)
	for _, r := range p {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
