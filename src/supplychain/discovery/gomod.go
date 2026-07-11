package discovery

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// goProxyLatest is the JSON response from proxy.golang.org/{mod}/@latest.
type goProxyLatest struct {
	Version string `json:"Version"`
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

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

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
		m.resolveGoModule(ctx, &deps[i])
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
func (m *Resolver) resolveGoModule(ctx context.Context, dep *supplychain.Dependency) {
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
	}

	// Under a patch-lock ceiling the deps layer needs the full version list to
	// re-target to the newest patch of the current minor (proxy @latest only gives
	// the single newest, which may be a minor). Gated behind FetchVersionLists so
	// the default path makes no extra request. Best-effort: a list failure leaves
	// AvailableVersions empty and the dep is simply held, as before.
	if m.cfg.FetchVersionLists {
		listURL := fmt.Sprintf("%s/%s/@v/list", strings.TrimRight(baseURL, "/"), escapeModPath(dep.Name))
		if data, err := m.http.fetchBytes(ctx, listURL, ep); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if v := strings.TrimSpace(line); v != "" {
					dep.AvailableVersions = append(dep.AvailableVersions, v)
				}
			}
		}
	}
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
