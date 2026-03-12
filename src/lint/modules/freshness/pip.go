package freshness

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// pypiResponse matches the PyPI JSON API response.
type pypiResponse struct {
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
}

// checkPip parses requirements.txt / Pipfile and resolves latest versions via PyPI.
func (m *freshnessModule) checkPip(ctx context.Context, file lint.FileInfo) ([]Dependency, error) {
	if !m.cfg.sourceEnabled(EcosystemPip) {
		return nil, nil
	}

	base := strings.ToLower(file.Path)
	if strings.HasSuffix(base, ".txt") {
		return m.parseRequirementsTxt(ctx, file)
	}
	if strings.HasSuffix(base, "pipfile") {
		return m.parsePipfile(ctx, file)
	}
	return nil, nil
}

// parseRequirementsTxt handles requirements.txt format.
func (m *freshnessModule) parseRequirementsTxt(ctx context.Context, file lint.FileInfo) ([]Dependency, error) {
	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var deps []Dependency
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}

		// Remove inline comments
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = line[:idx]
		}

		// Remove environment markers (e.g. ; python_version >= "3.6")
		if idx := strings.Index(line, ";"); idx >= 0 {
			line = line[:idx]
		}

		line = strings.TrimSpace(line)

		// Parse: package==version, package>=version, package~=version
		name, version := splitPipSpec(line)
		if name == "" {
			continue
		}

		dep := Dependency{
			Name:      name,
			Current:   version,
			Ecosystem: EcosystemPip,
			File:      file.Path,
			Line:      lineNum,
		}

		if version != "" {
			m.resolvePyPI(ctx, &dep)
		}

		deps = append(deps, dep)
	}

	return deps, scanner.Err()
}

// parsePipfile handles Pipfile format (basic TOML parsing for [packages]).
func (m *freshnessModule) parsePipfile(ctx context.Context, file lint.FileInfo) ([]Dependency, error) {
	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var deps []Dependency
	scanner := bufio.NewScanner(f)
	lineNum := 0
	inPackages := false

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "[packages]" || line == "[dev-packages]" {
			inPackages = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inPackages = false
			continue
		}

		if !inPackages || line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse: package = "==version" or package = "*"
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		spec := strings.TrimSpace(parts[1])
		spec = strings.Trim(spec, `"'`)

		version := ""
		if strings.HasPrefix(spec, "=") {
			version = strings.TrimLeft(spec, "=~<>!")
		}

		dep := Dependency{
			Name:      name,
			Current:   version,
			Ecosystem: EcosystemPip,
			File:      file.Path,
			Line:      lineNum,
		}

		if version != "" {
			m.resolvePyPI(ctx, &dep)
		}

		deps = append(deps, dep)
	}

	return deps, scanner.Err()
}

// splitPipSpec splits "package==1.0.0" into ("package", "1.0.0").
func splitPipSpec(spec string) (string, string) {
	// Try version specifiers in order of specificity
	for _, sep := range []string{"===", "==", "~=", "!=", ">=", "<=", ">", "<"} {
		if idx := strings.Index(spec, sep); idx >= 0 {
			name := strings.TrimSpace(spec[:idx])
			version := strings.TrimSpace(spec[idx+len(sep):])
			// For ranges, take the base version
			if commaIdx := strings.Index(version, ","); commaIdx >= 0 {
				version = version[:commaIdx]
			}
			return name, version
		}
	}
	// No version pinned
	return spec, ""
}

// resolvePyPI queries PyPI (or custom registry) for the latest version.
func (m *freshnessModule) resolvePyPI(ctx context.Context, dep *Dependency) {
	ep := m.cfg.registryEndpoint(EcosystemPip)
	baseURL := m.cfg.registryURL(EcosystemPip, "https://pypi.org/pypi")
	url := fmt.Sprintf("%s/%s/json", strings.TrimRight(baseURL, "/"), dep.Name)
	dep.SourceURL = url

	var resp pypiResponse
	if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
		return
	}
	if resp.Info.Version != "" {
		dep.Latest = resp.Info.Version
	}
}

// resolvePipPackages resolves pip packages found in Dockerfile RUN lines.
func (m *freshnessModule) resolvePipPackages(ctx context.Context, file lint.FileInfo, pkgs []packageRef) []Dependency {
	var deps []Dependency
	for _, pkg := range pkgs {
		if pkg.Version == "" {
			continue
		}

		dep := Dependency{
			Name:      pkg.Name,
			Current:   pkg.Version,
			Ecosystem: EcosystemPip,
			File:      file.Path,
			Line:      pkg.Line,
		}
		m.resolvePyPI(ctx, &dep)
		deps = append(deps, dep)
	}
	return deps
}
