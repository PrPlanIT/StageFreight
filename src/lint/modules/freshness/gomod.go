package freshness

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// goProxyLatest is the JSON response from proxy.golang.org/{mod}/@latest.
type goProxyLatest struct {
	Version string `json:"Version"`
}

// checkGoMod parses go.mod and resolves latest versions via proxy.golang.org.
func (m *freshnessModule) checkGoMod(ctx context.Context, file lint.FileInfo) ([]Dependency, error) {
	if !m.cfg.sourceEnabled(EcosystemGoMod) {
		return nil, nil
	}

	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var deps []Dependency
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
				dep := Dependency{
					Name:      parts[1],
					Current:   parts[2],
					Ecosystem: EcosystemGoMod,
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
			dep := Dependency{
				Name:      parts[0],
				Current:   parts[1],
				Ecosystem: EcosystemGoMod,
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
func (m *freshnessModule) resolveGoModule(ctx context.Context, dep *Dependency) {
	ep := m.cfg.registryEndpoint(EcosystemGoMod)
	baseURL := m.cfg.registryURL(EcosystemGoMod, "https://proxy.golang.org")
	url := fmt.Sprintf("%s/%s/@latest", strings.TrimRight(baseURL, "/"), dep.Name)
	dep.SourceURL = url

	var resp goProxyLatest
	if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
		return
	}
	if resp.Version != "" {
		dep.Latest = resp.Version
	}
}
