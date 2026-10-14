package manifest

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// spdxDoc is the part of an SPDX JSON SBOM the import reads.
type spdxDoc struct {
	Packages []struct {
		Name         string `json:"name"`
		VersionInfo  string `json:"versionInfo"`
		ExternalRefs []struct {
			ReferenceType    string `json:"referenceType"`
			ReferenceLocator string `json:"referenceLocator"`
		} `json:"externalRefs"`
	} `json:"packages"`
}

// ImportSBOM fills m.Detected from an SPDX JSON SBOM of the built image. A package's
// ecosystem comes from its package URL (pkg:pypi/..., pkg:npm/..., pkg:apk/...); packages
// in ecosystems Detected does not list are skipped.
func ImportSBOM(m *Manifest, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc spdxDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing SBOM %s: %w", path, err)
	}

	var d Detected
	seen := map[string]bool{}
	for _, p := range doc.Packages {
		manager, group := "", (*[]InvItem)(nil)
		for _, ref := range p.ExternalRefs {
			if ref.ReferenceType != "purl" {
				continue
			}
			switch purlType(ref.ReferenceLocator) {
			case "pypi":
				manager, group = "pip", &d.Pip
			case "npm":
				manager, group = "npm", &d.Npm
			case "apk":
				manager, group = "apk", &d.Apk
			case "deb":
				manager, group = "apt", &d.Apt
			case "golang":
				manager, group = "go", &d.Go
			case "gem":
				manager, group = "gem", &d.Gem
			}
			if group != nil {
				break
			}
		}
		if group == nil || p.Name == "" {
			continue
		}
		version := p.VersionInfo
		switch strings.ToUpper(version) {
		case "UNKNOWN", "NOASSERTION", "NONE":
			version = "" // SPDX and syft placeholders for "no version found"
		}
		key := manager + "\x00" + p.Name + "\x00" + version
		if seen[key] {
			continue
		}
		seen[key] = true
		*group = append(*group, InvItem{
			Name:       p.Name,
			Version:    version,
			Pinned:     version != "",
			Source:     "sbom",
			Manager:    manager,
			Confidence: "detected",
		})
	}

	m.Detected = d
	for _, g := range m.Detected.groups() {
		sortInvItems(*g)
	}
	format := "spdx-json"
	m.Security.SBOM = SBOMInfo{Present: true, Format: &format, Path: &path}
	m.Completeness.SBOMImported = true
	return nil
}

// purlType returns the type of a package URL ("pkg:pypi/frappe@16.51.0" → "pypi").
func purlType(purl string) string {
	rest, ok := strings.CutPrefix(purl, "pkg:")
	if !ok {
		return ""
	}
	t, _, _ := strings.Cut(rest, "/")
	t, _ = url.PathUnescape(t)
	return strings.ToLower(t)
}
