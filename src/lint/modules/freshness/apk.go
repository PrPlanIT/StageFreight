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

// checkAPK resolves Alpine APK package freshness by parsing APKINDEX.
func (m *freshnessModule) checkAPK(ctx context.Context, file lint.FileInfo, pkgs []packageRef, alpineVer string) []Dependency {
	// Parse the Alpine major.minor from the version string.
	v := parseVersion(alpineVer)
	if v == nil {
		return nil
	}
	branch := fmt.Sprintf("v%d.%d", v.Major(), v.Minor())

	// Fetch and parse APKINDEX for the main repository.
	ep := m.cfg.Registries.Alpine
	baseURL := m.cfg.registryURL(EcosystemAlpineAPK, "https://dl-cdn.alpinelinux.org/alpine")
	indexURL := fmt.Sprintf("%s/%s/main/x86_64/APKINDEX.tar.gz", strings.TrimRight(baseURL, "/"), branch)
	pkgVersions, err := m.fetchAPKIndex(ctx, indexURL, ep)
	if err != nil {
		return nil
	}

	// Also try the community repo.
	communityURL := fmt.Sprintf("%s/%s/community/x86_64/APKINDEX.tar.gz", strings.TrimRight(baseURL, "/"), branch)
	communityVersions, err := m.fetchAPKIndex(ctx, communityURL, ep)
	if err == nil {
		for k, v := range communityVersions {
			if _, exists := pkgVersions[k]; !exists {
				pkgVersions[k] = v
			}
		}
	}

	var deps []Dependency
	for _, pkg := range pkgs {
		if pkg.Version == "" {
			continue // unpinned — nothing to compare
		}

		repoVer, ok := pkgVersions[pkg.Name]
		if !ok {
			continue
		}

		deps = append(deps, Dependency{
			Name:      pkg.Name,
			Current:   pkg.Version,
			Latest:    repoVer,
			Ecosystem: EcosystemAlpineAPK,
			File:      file.Path,
			Line:      pkg.Line,
			SourceURL: indexURL,
		})
	}

	return deps
}

// fetchAPKIndex downloads and parses an APKINDEX.tar.gz file.
// Returns a map of package name → version.
func (m *freshnessModule) fetchAPKIndex(ctx context.Context, url string, ep *RegistryEndpoint) (map[string]string, error) {
	data, err := m.http.fetchBytes(ctx, url, ep)
	if err != nil {
		return nil, err
	}

	// APKINDEX.tar.gz contains a gzipped tar with an APKINDEX file.
	// The APKINDEX itself is a simple field-per-line format separated
	// by blank lines. We only need P: (package) and V: (version).
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("freshness: decompress APKINDEX: %w", err)
	}
	defer gz.Close()

	// The tar archive contains APKINDEX as the first useful entry.
	// Rather than properly parsing tar, we can decompress and scan
	// for the P:/V: fields since the format is simple text.
	content, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("freshness: read APKINDEX: %w", err)
	}

	return parseAPKIndex(content), nil
}

// parseAPKIndex parses the APKINDEX field-per-line format.
// Records are separated by blank lines. Fields:
//
//	P:package-name
//	V:version
func parseAPKIndex(data []byte) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))

	var currentPkg, currentVer string
	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// End of record
			if currentPkg != "" && currentVer != "" {
				result[currentPkg] = currentVer
			}
			currentPkg = ""
			currentVer = ""
			continue
		}

		if strings.HasPrefix(line, "P:") {
			currentPkg = line[2:]
		} else if strings.HasPrefix(line, "V:") {
			currentVer = line[2:]
		}
	}

	// Flush last record
	if currentPkg != "" && currentVer != "" {
		result[currentPkg] = currentVer
	}

	return result
}
