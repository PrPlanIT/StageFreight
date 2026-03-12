package freshness

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// checkAPT resolves Debian/Ubuntu APT package freshness by parsing Packages.gz.
func (m *freshnessModule) checkAPT(ctx context.Context, file lint.FileInfo, pkgs []packageRef, distro, codename string) []Dependency {
	url := m.aptRepoURL(distro, codename)
	if url == "" {
		return nil
	}

	ep := m.aptEndpoint(distro)
	pkgVersions, err := m.fetchPackagesGz(ctx, url, ep)
	if err != nil {
		return nil
	}

	var deps []Dependency
	for _, pkg := range pkgs {
		if pkg.Version == "" {
			continue // unpinned
		}

		repoVer, ok := pkgVersions[pkg.Name]
		if !ok {
			continue
		}

		deps = append(deps, Dependency{
			Name:      pkg.Name,
			Current:   pkg.Version,
			Latest:    repoVer,
			Ecosystem: EcosystemDebianAPT,
			File:      file.Path,
			Line:      pkg.Line,
			SourceURL: url,
		})
	}

	return deps
}

// aptRepoURL returns the Packages.gz URL, using custom registry if configured.
func (m *freshnessModule) aptRepoURL(distro, codename string) string {
	switch distro {
	case "debian":
		ep := m.cfg.Registries.Debian
		if ep != nil && ep.URL != "" {
			return fmt.Sprintf("%s/dists/%s/main/binary-amd64/Packages.gz", strings.TrimRight(ep.URL, "/"), codename)
		}
		return fmt.Sprintf("http://deb.debian.org/debian/dists/%s/main/binary-amd64/Packages.gz", codename)
	case "ubuntu":
		ep := m.cfg.Registries.Ubuntu
		if ep != nil && ep.URL != "" {
			return fmt.Sprintf("%s/dists/%s/main/binary-amd64/Packages.gz", strings.TrimRight(ep.URL, "/"), codename)
		}
		return fmt.Sprintf("http://archive.ubuntu.com/ubuntu/dists/%s/main/binary-amd64/Packages.gz", codename)
	default:
		return ""
	}
}

// aptEndpoint returns the registry endpoint for the distro.
func (m *freshnessModule) aptEndpoint(distro string) *RegistryEndpoint {
	switch distro {
	case "debian":
		return m.cfg.Registries.Debian
	case "ubuntu":
		return m.cfg.Registries.Ubuntu
	default:
		return nil
	}
}

// fetchPackagesGz downloads and parses a Packages.gz file.
// Returns a map of package name → version.
func (m *freshnessModule) fetchPackagesGz(ctx context.Context, url string, ep *RegistryEndpoint) (map[string]string, error) {
	data, err := m.http.fetchBytes(ctx, url, ep)
	if err != nil {
		return nil, err
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("freshness: decompress Packages.gz: %w", err)
	}
	defer gz.Close()

	content, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("freshness: read Packages.gz: %w", err)
	}

	return parsePackagesFile(content), nil
}

// parsePackagesFile parses the Debian Packages file format.
// Records are separated by blank lines. Fields:
//
//	Package: name
//	Version: version
func parsePackagesFile(data []byte) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))

	var currentPkg, currentVer string
	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if currentPkg != "" && currentVer != "" {
				result[currentPkg] = currentVer
			}
			currentPkg = ""
			currentVer = ""
			continue
		}

		if strings.HasPrefix(line, "Package: ") {
			currentPkg = line[9:]
		} else if strings.HasPrefix(line, "Version: ") {
			currentVer = line[9:]
		}
	}

	// Flush last record
	if currentPkg != "" && currentVer != "" {
		result[currentPkg] = currentVer
	}

	return result
}
