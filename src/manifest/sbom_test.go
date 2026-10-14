package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportSBOMSortsPackagesByEcosystem(t *testing.T) {
	sbom := `{"packages":[
	  {"name":"frappe","versionInfo":"16.51.0","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:pypi/frappe@16.51.0"}]},
	  {"name":"erpnext","versionInfo":"16.50.0","externalRefs":[{"referenceCategory":"SECURITY","referenceType":"cpe23Type","referenceLocator":"cpe:2.3:a:erpnext"},{"referenceType":"purl","referenceLocator":"pkg:pypi/erpnext@16.50.0"}]},
	  {"name":"frappe","versionInfo":"16.51.0","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:pypi/frappe@16.51.0"}]},
	  {"name":"busybox","versionInfo":"1.37.0-r30","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:apk/alpine/busybox@1.37.0-r30"}]},
	  {"name":"redis","versionInfo":"UNKNOWN","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:npm/redis"}]},
	  {"name":"image-root","versionInfo":"","externalRefs":[]}
	]}`
	path := filepath.Join(t.TempDir(), "sbom.spdx.json")
	if err := os.WriteFile(path, []byte(sbom), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{}
	if err := ImportSBOM(m, path); err != nil {
		t.Fatal(err)
	}
	if len(m.Detected.Pip) != 2 || m.Detected.Pip[0].Name != "erpnext" || m.Detected.Pip[1].Name != "frappe" {
		t.Errorf("pip: got %+v", m.Detected.Pip)
	}
	if len(m.Detected.Apk) != 1 || len(m.Detected.Npm) != 1 || m.Detected.Pip[0].Manager != "pip" {
		t.Errorf("apk/npm: got %+v / %+v", m.Detected.Apk, m.Detected.Npm)
	}
	if m.Detected.Npm[0].Version != "" || m.Detected.Npm[0].Pinned {
		t.Errorf("a placeholder version is no version: got %+v", m.Detected.Npm[0])
	}
	if !m.Completeness.SBOMImported || !m.Security.SBOM.Present {
		t.Error("the import is recorded on the manifest")
	}
	if len(m.Inventories.Pip) != 0 {
		t.Error("declared inventories are untouched")
	}
}

func TestDetectedSectionRendersWithInclude(t *testing.T) {
	m := &Manifest{Detected: Detected{Pip: []InvItem{
		{Name: "anyio", Version: "4.0"}, {Name: "frappe", Version: "16.51.0"}, {Name: "erpnext", Version: "16.50.0"},
	}}}
	data, err := ExtractSection(m, "detected.pip")
	if err != nil {
		t.Fatal(err)
	}
	got, err := FilterItems(data, []string{"frappe", "erpnext"})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := got.([]interface{})
	if !ok || len(items) != 2 || items[0].(map[string]interface{})["name"] != "frappe" {
		t.Errorf("include picks named items in the order listed: got %v", got)
	}
	if _, err := ExtractSection(&Manifest{}, "detected.apt"); err != nil {
		t.Errorf("an empty detected section resolves, so it renders as no items: %v", err)
	}
}

func TestDetectedItemsAreNotLinkedToAGuessedRegistry(t *testing.T) {
	if got := inventoryItemLink(map[string]interface{}{"name": "crm", "manager": "pip", "source": "sbom"}); got != "" {
		t.Errorf("detected: got %q", got)
	}
	if got := inventoryItemLink(map[string]interface{}{"name": "requests", "manager": "pip", "source": "dockerfile"}); got == "" {
		t.Error("a declared pip install keeps its PyPI link")
	}
}
