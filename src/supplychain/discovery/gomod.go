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
	scanner := bufio.NewScanner(f)
	lineNum := 0
	inRequire := false

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

	// Resolve latest versions
	for i := range deps {
		// Skip indirect by default
		if deps[i].Indirect {
			continue
		}
		m.resolveGoModule(ctx, &deps[i])
	}

	return deps, nil
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
